package valkey

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"os"

	"github.com/redis/go-redis/v9"
)

type DaySchedule struct {
	Text  string `json:"text"`
	Emoji string `json:"emoji"`
}

type UserPrefs struct {
	DisableDM bool `json:"disable_dm"`
}

type UserData struct {
	Schedule map[string]DaySchedule `json:"schedule"`
	Prefs    UserPrefs              `json:"prefs"`
}

type Client struct {
	rdb *redis.Client
}

func New() *Client {
	const instance = "GOD_MORGEN"

	host := envOr("VALKEY_HOST_"+instance, envOr("VALKEY_HOST", "localhost"))
	port := envOr("VALKEY_PORT_"+instance, envOr("VALKEY_PORT", "6379"))
	username := envOr("VALKEY_USERNAME_"+instance, envOr("VALKEY_USERNAME", ""))
	password := envOr("VALKEY_PASSWORD_"+instance, envOr("VALKEY_PASSWORD", ""))
	_, useSSL := os.LookupEnv("VALKEY_HOST_" + instance)

	opts := &redis.Options{
		Addr:     fmt.Sprintf("%s:%s", host, port),
		Username: username,
		Password: password,
	}
	if useSSL {
		opts.TLSConfig = &tls.Config{}
	}

	return &Client{rdb: redis.NewClient(opts)}
}

func (c *Client) SaveUserData(ctx context.Context, userID string, data UserData) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return c.rdb.Set(ctx, userID, b, 0).Err()
}

func (c *Client) GetUserData(ctx context.Context, userID string) (UserData, error) {
	b, err := c.rdb.Get(ctx, userID).Bytes()
	if err == redis.Nil {
		return UserData{}, nil
	}
	if err != nil {
		return UserData{}, err
	}
	var data UserData
	if err := json.Unmarshal(b, &data); err != nil {
		return UserData{}, err
	}
	return data, nil
}

func (c *Client) MigrateUserData(ctx context.Context) error {
	userIDs, err := c.AllUserIDs(ctx)
	if err != nil {
		return err
	}
	for _, userID := range userIDs {
		b, err := c.rdb.Get(ctx, userID).Bytes()
		if err != nil {
			continue
		}
		var data UserData
		if err := json.Unmarshal(b, &data); err != nil || data.Schedule != nil {
			continue
		}
		var oldSchedule map[string]DaySchedule
		if err := json.Unmarshal(b, &oldSchedule); err != nil || len(oldSchedule) == 0 {
			continue
		}
		data.Schedule = oldSchedule
		_ = c.SaveUserData(ctx, userID, data)
	}
	return nil
}

func (c *Client) DeleteUserData(ctx context.Context, userID string) error {
	return c.rdb.Del(ctx, userID).Err()
}

func (c *Client) AllUserIDs(ctx context.Context) ([]string, error) {
	return c.rdb.Keys(ctx, "*").Result()
}

func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}
