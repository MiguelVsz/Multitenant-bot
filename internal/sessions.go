package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type ConversationSession struct {
	TenantID     int64             `json:"tenant_id"`
	UserID       string            `json:"user_id"`
	CurrentState string            `json:"current_state"`
	Context      map[string]string `json:"context"`
	History      []AIMessage       `json:"history"`
	UpdatedAt    time.Time         `json:"updated_at"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

type SessionStore struct {
	rdb *redis.Client
	ttl time.Duration
}

func NewSessionStore(rdb *redis.Client, ttl time.Duration) *SessionStore {
	return &SessionStore{rdb: rdb, ttl: ttl}
}

func (s *SessionStore) Load(ctx context.Context, tenantID int64, userID string) (*ConversationSession, error) {
	key := s.key(tenantID, userID)
	raw, err := s.rdb.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return &ConversationSession{
				TenantID:     tenantID,
				UserID:       userID,
				CurrentState: "IDLE",
				Context:      map[string]string{},
				History:      []AIMessage{},
				UpdatedAt:    time.Now().UTC(),
			}, nil
		}
		return nil, err
	}

	var session ConversationSession
	if err := json.Unmarshal([]byte(raw), &session); err != nil {
		return nil, err
	}
	if session.Context == nil {
		session.Context = map[string]string{}
	}
	if session.History == nil {
		session.History = []AIMessage{}
	}
	return &session, nil
}

func (s *SessionStore) Save(ctx context.Context, session *ConversationSession) error {
	session.UpdatedAt = time.Now().UTC()
	if session.Context == nil {
		session.Context = map[string]string{}
	}
	if session.History == nil {
		session.History = []AIMessage{}
	}

	payload, err := json.Marshal(session)
	if err != nil {
		return err
	}

	return s.rdb.Set(ctx, s.key(session.TenantID, session.UserID), payload, s.ttl).Err()
}

func (s *SessionStore) Delete(ctx context.Context, tenantID int64, userID string) error {
	return s.rdb.Del(ctx, s.key(tenantID, userID)).Err()
}

func (s *SessionStore) key(tenantID int64, userID string) string {
	return fmt.Sprintf("session:%d:%s", tenantID, userID)
}
