package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const defaultAgentSessionTTL = 24 * time.Hour

type agentSessionStore struct {
	rdb *redis.Client
	ttl time.Duration
}

func newAgentSessionStoreFromEnv() (*agentSessionStore, error) {
	rawURL := strings.TrimSpace(os.Getenv("AGENT_REDIS_URL"))
	if rawURL == "" {
		rawURL = strings.TrimSpace(os.Getenv("REDIS_URL"))
	}
	if rawURL == "" {
		return nil, nil
	}

	opts, err := redis.ParseURL(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}

	client := redis.NewClient(opts)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("ping redis: %w", err)
	}

	// Reutilizamos el mismo cliente para delivery helpers.
	rdb = client

	return &agentSessionStore{
		rdb: client,
		ttl: defaultAgentSessionTTL,
	}, nil
}

func (s *agentSessionStore) Load(ctx context.Context, userID string) (*consoleSession, error) {
	if s == nil || s.rdb == nil {
		return &consoleSession{}, nil
	}

	raw, err := s.rdb.Get(ctx, s.key(userID)).Result()
	if err != nil {
		if err == redis.Nil {
			return &consoleSession{}, nil
		}
		return nil, err
	}

	var session consoleSession
	if err := json.Unmarshal([]byte(raw), &session); err != nil {
		return nil, err
	}
	if session.CurrentContext == "" {
		session.CurrentContext = ""
	}
	return &session, nil
}

func (s *agentSessionStore) Save(ctx context.Context, userID string, session *consoleSession) error {
	if s == nil || s.rdb == nil || session == nil {
		return nil
	}

	payload, err := json.Marshal(session)
	if err != nil {
		return err
	}

	return s.rdb.Set(ctx, s.key(userID), payload, s.ttl).Err()
}

func (s *agentSessionStore) Delete(ctx context.Context, userID string) error {
	if s == nil || s.rdb == nil {
		return nil
	}
	return s.rdb.Del(ctx, s.key(userID)).Err()
}

func (s *agentSessionStore) key(userID string) string {
	return "agent-session:" + userID
}
