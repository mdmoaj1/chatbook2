package presence

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

// Service manages user presence (online/offline/away) via Redis.
//
// Keys:
//   presence:{user_id}  →  "online" | TTL 35s (heartbeat every 30s)
//
// When a user's WebSocket disconnects, presence key expires naturally.
// We also explicitly delete it on disconnect.

type Service struct {
	rdb *redis.Client
}

func NewService(rdb *redis.Client) *Service {
	return &Service{rdb: rdb}
}

const (
	presenceTTL     = 35 * time.Second
	presenceKeyPfx  = "presence:"
)

func (s *Service) SetOnline(userID string) {
	ctx := context.Background()
	key := presenceKeyPfx + userID
	if err := s.rdb.Set(ctx, key, "online", presenceTTL).Err(); err != nil {
		log.Error().Err(err).Str("userID", userID).Msg("Failed to set online presence")
	}
}

func (s *Service) SetOffline(userID string) {
	ctx := context.Background()
	key := presenceKeyPfx + userID
	s.rdb.Del(ctx, key)
}

// Heartbeat refreshes TTL — called every 30s by the WS client pump
func (s *Service) Heartbeat(userID string) {
	ctx := context.Background()
	key := presenceKeyPfx + userID
	s.rdb.Expire(ctx, key, presenceTTL)
}

// IsOnline checks if user has an active presence key in Redis.
// Works across multiple backend pods (unlike Hub.IsOnline which is local).
func (s *Service) IsOnline(userID string) bool {
	ctx := context.Background()
	key := presenceKeyPfx + userID
	result, err := s.rdb.Exists(ctx, key).Result()
	if err != nil {
		return false
	}
	return result > 0
}

// GetOnlineUsers returns subset of userIDs that are currently online.
func (s *Service) GetOnlineUsers(userIDs []string) []string {
	if len(userIDs) == 0 {
		return nil
	}

	ctx := context.Background()
	pipe := s.rdb.Pipeline()
	cmds := make([]*redis.IntCmd, len(userIDs))
	for i, uid := range userIDs {
		cmds[i] = pipe.Exists(ctx, presenceKeyPfx+uid)
	}
	pipe.Exec(ctx)

	online := make([]string, 0, len(userIDs))
	for i, cmd := range cmds {
		if cmd.Val() > 0 {
			online = append(online, userIDs[i])
		}
	}
	return online
}
