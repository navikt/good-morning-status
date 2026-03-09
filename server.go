package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/navikt/god-morgen/internal/slack"
	"github.com/navikt/god-morgen/internal/valkey"
)

type server struct {
	valkey *valkey.Client
	slack  *slack.Client
	log    *slog.Logger
}

func newServer(log *slog.Logger) *server {
	return &server{
		valkey: valkey.New(),
		slack:  slack.New(log, os.Getenv("SLACK_USER_TOKEN"), os.Getenv("SLACK_BOT_TOKEN")),
		log:    log,
	}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /slack/interactions", s.handleInteractions)
	mux.HandleFunc("POST /slack/commands", s.handleCommands)
	mux.HandleFunc("GET /internal/", s.handleInternal)
	mux.HandleFunc("POST /api/apply-statuses", s.handleApplyStatuses)
	return mux
}

func (s *server) handleInteractions(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(r.FormValue("payload")), &payload); err != nil {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}

	switch payload["type"] {
	case "view_submission":
		s.handleFormSubmission(w, r.Context(), payload)
	case "block_actions":
		s.handleBlockActions(w, r.Context(), payload)
	default:
		w.WriteHeader(http.StatusOK)
	}
}

func (s *server) handleCommands(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	triggerID := r.FormValue("trigger_id")
	userID := r.FormValue("user_id")
	text := strings.TrimSpace(r.FormValue("text"))

	if text == "unsubscribe" {
		if err := s.valkey.DeleteUserData(r.Context(), userID); err != nil {
			s.log.Error("delete_user_data_failed", "user_id", userID, "error", err)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"response_type": "ephemeral",
			"text":          "Du er nå avmeldt fra en god morgen.",
		})
		return
	}

	userData, err := s.valkey.GetUserData(r.Context(), userID)
	if err != nil {
		s.log.Error("get_user_data_failed", "user_id", userID, "error", err)
	}

	result, err := s.slack.OpenModal(triggerID, modalView(userData))
	if err != nil {
		s.log.Error("open_modal_failed", "error", err)
	} else if ok, _ := result["ok"].(bool); !ok {
		s.log.Error("open_modal_failed", "error", result["error"], "payload", result)
	}

	w.WriteHeader(http.StatusOK)
}

func (s *server) handleFormSubmission(w http.ResponseWriter, ctx context.Context, payload map[string]any) {
	user, _ := payload["user"].(map[string]any)
	userID, _ := user["id"].(string)
	view, _ := payload["view"].(map[string]any)
	state, _ := view["state"].(map[string]any)
	values, _ := state["values"].(map[string]any)

	days := []string{"monday", "tuesday", "wednesday", "thursday", "friday"}
	schedule := make(map[string]valkey.DaySchedule, len(days))
	for _, day := range days {
		textBlock, _ := values[day+"_text"].(map[string]any)
		textInput, _ := textBlock[day+"_text_input"].(map[string]any)
		text, _ := textInput["value"].(string)

		emojiBlock, _ := values[day+"_emoji"].(map[string]any)
		emojiInput, _ := emojiBlock[day+"_emoji_input"].(map[string]any)
		emoji := extractEmoji(emojiInput)

		schedule[day] = valkey.DaySchedule{Text: text, Emoji: emoji}
	}

	prefsBlock, _ := values["preferences"].(map[string]any)
	disableDMInput, _ := prefsBlock["disable_dm_checkbox"].(map[string]any)
	selectedOptions, _ := disableDMInput["selected_options"].([]any)
	disableDM := false
	for _, opt := range selectedOptions {
		if o, ok := opt.(map[string]any); ok {
			if o["value"] == "disable_dm" {
				disableDM = true
			}
		}
	}

	if err := s.valkey.SaveUserData(ctx, userID, valkey.UserData{
		Schedule: schedule,
		Prefs:    valkey.UserPrefs{DisableDM: disableDM},
	}); err != nil {
		s.log.Error("save_user_data_failed", "user_id", userID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"response_action": "clear"})
}

func (s *server) handleBlockActions(w http.ResponseWriter, ctx context.Context, payload map[string]any) {
	user, _ := payload["user"].(map[string]any)
	userID, _ := user["id"].(string)
	actions, _ := payload["actions"].([]any)
	if userID == "" || len(actions) == 0 {
		w.WriteHeader(http.StatusOK)
		return
	}

	action, _ := actions[0].(map[string]any)
	if action["action_id"] != "unsubscribe_button" {
		w.WriteHeader(http.StatusOK)
		return
	}

	s.processUnsubscribe(ctx, userID)
	w.WriteHeader(http.StatusOK)
}

func (s *server) processUnsubscribe(ctx context.Context, userID string) {
	if err := s.valkey.DeleteUserData(ctx, userID); err != nil {
		s.log.Error("delete_user_data_failed", "user_id", userID, "error", err)
	}
	s.log.Info("user_unsubscribed", "user_id", userID)

	dmResult, err := s.slack.SendDM(userID, "Du er nå avmeldt fra en god morgen.")
	if err != nil {
		s.log.Error("user_unsubscribed_dm_failed", "user_id", userID, "error", err)
		return
	}
	if ok, _ := dmResult["ok"].(bool); ok {
		s.log.Info("user_unsubscribed_dm_sent", "user_id", userID)
	} else {
		s.log.Error("user_unsubscribed_dm_failed", "user_id", userID, "error", dmResult["error"], "payload", dmResult)
	}
}

func (s *server) handleInternal(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userIDs, err := s.valkey.AllUserIDs(ctx)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var rows strings.Builder
	for _, userID := range userIDs {
		userData, _ := s.valkey.GetUserData(ctx, userID)
		var scheduleHTML string
		if userData.Schedule != nil {
			data, _ := json.MarshalIndent(userData.Schedule, "", "  ")
			scheduleHTML = "<pre>" + html.EscapeString(string(data)) + "</pre>"
		} else {
			scheduleHTML = "&mdash;"
		}
		fmt.Fprintf(&rows, "<tr><td>%s</td><td>%s</td></tr>\n", html.EscapeString(userID), scheduleHTML)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html lang="no">
  <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width,initial-scale=1">
    <title>God morgen - Intern</title>
    <style>
      body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial; padding: 20px; }
      table { border-collapse: collapse; width: 100%%; }
      th, td { border: 1px solid #ccc; padding: 6px; text-align: left; vertical-align: top; }
      pre { margin: 0; white-space: pre-wrap; word-wrap: break-word; }
    </style>
  </head>
  <body>
    <h1>God morgen — Intern</h1>
    <table>
      <thead><tr><th>Bruker</th><th>Plan</th></tr></thead>
      <tbody>
%s      </tbody>
    </table>
  </body>
</html>`, rows.String())
}

func (s *server) handleApplyStatuses(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	today := strings.ToLower(time.Now().Weekday().String())

	userIDs, err := s.valkey.AllUserIDs(ctx)
	if err != nil {
		s.log.Error("all_user_ids_failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	s.log.Info("apply_statuses", "user_count", len(userIDs))
	applied := 0

	for _, userID := range userIDs {
		userData, err := s.valkey.GetUserData(ctx, userID)
		if err != nil || userData.Schedule == nil {
			continue
		}

		dayConfig, ok := userData.Schedule[today]
		if !ok {
			continue
		}

		result, err := s.slack.SetStatus(userID, dayConfig.Text, dayConfig.Emoji)
		if err != nil {
			s.log.Error("set_status_failed", "user_id", userID, "error", err)
			continue
		}

		if ok, _ := result["ok"].(bool); !ok {
			s.log.Error("set_status_response", "user_id", userID, "error", result["error"])
			continue
		}
		s.log.Info("set_status_response", "user_id", userID)

		if userData.Prefs.DisableDM {
			applied++
			continue
		}

		dmResult, err := s.slack.SendDM(userID, fmt.Sprintf("God morgen! Status satt til %s %s", dayConfig.Emoji, dayConfig.Text))
		if err != nil {
			s.log.Error("send_dm_failed", "user_id", userID, "error", err)
			continue
		}
		if ok, _ := dmResult["ok"].(bool); ok {
			s.log.Info("send_dm_response", "user_id", userID)
			applied++
		} else {
			s.log.Error("send_dm_response", "user_id", userID, "error", dmResult["error"], "payload", dmResult)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"applied": applied,
		"users":   len(userIDs),
	})
}

func extractEmoji(richText map[string]any) string {
	if richText == nil {
		return ""
	}
	rtValue, _ := richText["rich_text_value"].(map[string]any)
	if rtValue == nil {
		return ""
	}
	sections, _ := rtValue["elements"].([]any)
	for _, s := range sections {
		section, _ := s.(map[string]any)
		if section == nil {
			continue
		}
		elements, _ := section["elements"].([]any)
		for _, e := range elements {
			el, _ := e.(map[string]any)
			if el == nil {
				continue
			}
			if el["type"] == "emoji" {
				if name, ok := el["name"].(string); ok {
					return ":" + name + ":"
				}
			}
		}
	}
	return ""
}

func modalView(userData valkey.UserData) map[string]any {
	type day struct{ id, label string }
	days := []day{
		{"monday", "Mandag"},
		{"tuesday", "Tirsdag"},
		{"wednesday", "Onsdag"},
		{"thursday", "Torsdag"},
		{"friday", "Fredag"},
	}

	blocks := []any{
		map[string]any{
			"type": "section",
			"text": map[string]any{
				"type": "mrkdwn",
				"text": "Sett opp din ukentlige statusplan! For hver dag kan du legge til en fast beskrivelse, og \"velge\" en emoji 🎉",
			},
		},
		map[string]any{"type": "divider"},
	}

	for _, d := range days {
		var ds *valkey.DaySchedule
		if userData.Schedule != nil {
			if v, ok := userData.Schedule[d.id]; ok {
				ds = &v
			}
		}
		blocks = append(blocks, dayBlocks(d.id, d.label, ds)...)
	}

	blocks = append(blocks,
		map[string]any{"type": "divider"},
		map[string]any{
			"type":     "input",
			"block_id": "preferences",
			"optional": true,
			"label": map[string]any{
				"type": "plain_text",
				"text": "Innstillinger",
			},
			"element": map[string]any{
				"type":      "checkboxes",
				"action_id": "disable_dm_checkbox",
				"options": []any{
					map[string]any{
						"text": map[string]any{
							"type": "plain_text",
							"text": "Ikke send meg en DM når status settes",
						},
						"value": "disable_dm",
					},
				},
				"initial_options": func() []any {
					if userData.Prefs.DisableDM {
						return []any{
							map[string]any{
								"text": map[string]any{
									"type": "plain_text",
									"text": "Ikke send meg en DM når status settes",
								},
								"value": "disable_dm",
							},
						}
					}
					return nil
				}(),
			},
		},
		map[string]any{"type": "divider"},
		map[string]any{
			"type": "section",
			"text": map[string]any{
				"type": "mrkdwn",
				"text": "*Slett data?* Hvis du ikke lenger ønsker å få status satt automatisk kan vi slette dataen din.",
			},
			"accessory": map[string]any{
				"type": "button",
				"text": map[string]any{
					"type":  "plain_text",
					"text":  "Slett meg",
					"emoji": true,
				},
				"style":     "danger",
				"action_id": "unsubscribe_button",
			},
		},
	)

	return map[string]any{
		"type":        "modal",
		"callback_id": "schedule_modal",
		"title":       map[string]any{"type": "plain_text", "text": "Ukentlig status"},
		"submit":      map[string]any{"type": "plain_text", "text": "Lagre"},
		"close":       map[string]any{"type": "plain_text", "text": "Avbryt"},
		"blocks":      blocks,
	}
}

func dayBlocks(day, label string, schedule *valkey.DaySchedule) []any {
	textElement := map[string]any{
		"type":      "plain_text_input",
		"action_id": day + "_text_input",
		"placeholder": map[string]any{
			"type": "plain_text",
			"text": "f.eks. Hjemmekontor",
		},
	}
	if schedule != nil && schedule.Text != "" {
		textElement["initial_value"] = schedule.Text
	}

	emojiElement := map[string]any{
		"type":      "rich_text_input",
		"action_id": day + "_emoji_input",
	}
	if schedule != nil && schedule.Emoji != "" {
		emojiName := strings.Trim(schedule.Emoji, ":")
		emojiElement["initial_value"] = map[string]any{
			"type": "rich_text",
			"elements": []any{
				map[string]any{
					"type": "rich_text_section",
					"elements": []any{
						map[string]any{"type": "emoji", "name": emojiName},
					},
				},
			},
		}
	}

	return []any{
		map[string]any{
			"type": "header",
			"text": map[string]any{"type": "plain_text", "text": label},
		},
		map[string]any{
			"type":     "input",
			"block_id": day + "_text",
			"label":    map[string]any{"type": "plain_text", "text": "Statusbeskrivelse"},
			"element":  textElement,
		},
		map[string]any{
			"type":     "input",
			"block_id": day + "_emoji",
			"label":    map[string]any{"type": "plain_text", "text": "Statusemoji"},
			"element":  emojiElement,
		},
	}
}
