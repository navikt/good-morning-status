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

func (c *Client) SaveSchedule(ctx context.Context, userID string, schedule map[string]DaySchedule) error {
	data, err := json.Marshal(schedule)
	if err != nil {
		return err
	}
	return c.rdb.Set(ctx, userID, data, 0).Err()
}

func (c *Client) GetSchedule(ctx context.Context, userID string) (map[string]DaySchedule, error) {
	data, err := c.rdb.Get(ctx, userID).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var schedule map[string]DaySchedule
	if err := json.Unmarshal(data, &schedule); err != nil {
		return nil, err
	}
	return schedule, nil
}

func (c *Client) DeleteSchedule(ctx context.Context, userID string) error {
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
