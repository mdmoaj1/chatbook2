package messaging

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/chatbook/backend/internal/notification"
	"github.com/chatbook/backend/internal/presence"
	"github.com/chatbook/backend/internal/websocket"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// ─── Models ──────────────────────────────────────────────────────────────────

type Message struct {
	ID               string    `json:"id"`
	SenderID         string    `json:"sender_id"`
	RecipientID      string    `json:"recipient_id,omitempty"`
	GroupID          string    `json:"group_id,omitempty"`
	ContentEncrypted string    `json:"content_encrypted"`
	ContentIV        string    `json:"content_iv"`
	DHPublicKey      string    `json:"dh_public_key,omitempty"`
	MessageIndex     int       `json:"message_index"`
	MessageType      string    `json:"message_type"`
	Status           string    `json:"status"`
	SentAt           time.Time `json:"sent_at"`
	DeliveredAt      *time.Time `json:"delivered_at,omitempty"`
	ReadAt           *time.Time `json:"read_at,omitempty"`
}

// ─── Repository ──────────────────────────────────────────────────────────────

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Save(ctx context.Context, msg *Message) error {
	if msg.ID == "" {
		msg.ID = uuid.NewString()
	}
	msg.SentAt = time.Now()
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO messages
		  (id, sender_id, recipient_id, group_id, content_encrypted, content_iv,
		   dh_public_key, message_index, message_type, status, sent_at)
		VALUES ($1,$2,NULLIF($3,'')::UUID,NULLIF($4,'')::UUID,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (id) DO NOTHING
	`, msg.ID, msg.SenderID, msg.RecipientID, msg.GroupID,
		msg.ContentEncrypted, msg.ContentIV, msg.DHPublicKey,
		msg.MessageIndex, msg.MessageType, msg.Status, msg.SentAt)
	return err
}

func (r *Repository) GetConversation(ctx context.Context, userA, userB string, limit int, before time.Time) ([]Message, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, sender_id, COALESCE(recipient_id::TEXT,''), COALESCE(group_id::TEXT,''),
		       content_encrypted, content_iv, COALESCE(dh_public_key,''),
		       message_index, message_type, status, sent_at,
		       delivered_at, read_at
		FROM messages
		WHERE sent_at < $3
		  AND ((sender_id = $1 AND recipient_id = $2::UUID)
		    OR (sender_id = $2 AND recipient_id = $1::UUID))
		ORDER BY sent_at DESC
		LIMIT $4
	`, userA, userB, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.SenderID, &m.RecipientID, &m.GroupID,
			&m.ContentEncrypted, &m.ContentIV, &m.DHPublicKey,
			&m.MessageIndex, &m.MessageType, &m.Status, &m.SentAt,
			&m.DeliveredAt, &m.ReadAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

func (r *Repository) MarkDelivered(ctx context.Context, msgID string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE messages SET status='DELIVERED', delivered_at=NOW() WHERE id=$1 AND delivered_at IS NULL
	`, msgID)
	return err
}

func (r *Repository) MarkRead(ctx context.Context, msgID string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE messages SET status='READ', read_at=NOW() WHERE id=$1 AND read_at IS NULL
	`, msgID)
	return err
}

func (r *Repository) QueueOffline(ctx context.Context, msgID, userID string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO offline_message_queue (message_id, user_id)
		VALUES ($1, $2)
		ON CONFLICT (message_id, user_id) DO NOTHING
	`, msgID, userID)
	return err
}

func (r *Repository) GetOfflineMessages(ctx context.Context, userID string) ([]Message, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT m.id, m.sender_id, COALESCE(m.recipient_id::TEXT,''), COALESCE(m.group_id::TEXT,''),
		       m.content_encrypted, m.content_iv, COALESCE(m.dh_public_key,''),
		       m.message_index, m.message_type, m.status, m.sent_at,
		       m.delivered_at, m.read_at
		FROM messages m
		JOIN offline_message_queue q ON q.message_id = m.id
		WHERE q.user_id = $1
		ORDER BY m.sent_at ASC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.SenderID, &m.RecipientID, &m.GroupID,
			&m.ContentEncrypted, &m.ContentIV, &m.DHPublicKey,
			&m.MessageIndex, &m.MessageType, &m.Status, &m.SentAt,
			&m.DeliveredAt, &m.ReadAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}

	// Clear delivered offline messages
	if len(msgs) > 0 {
		r.db.ExecContext(ctx, `DELETE FROM offline_message_queue WHERE user_id = $1`, userID)
	}
	return msgs, rows.Err()
}

// GetFCMTokens returns all active FCM tokens for a user
func (r *Repository) GetFCMTokens(ctx context.Context, userID string) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT fcm_token FROM devices
		WHERE user_id = $1 AND fcm_token IS NOT NULL
		  AND last_seen > NOW() - INTERVAL '30 days'
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tokens []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		tokens = append(tokens, t)
	}
	return tokens, rows.Err()
}

// GetDisplayName returns a user's display name for notifications
func (r *Repository) GetDisplayName(ctx context.Context, userID string) string {
	var name string
	r.db.QueryRowContext(ctx, `SELECT display_name FROM users WHERE id = $1`, userID).Scan(&name)
	return name
}

// ─── Service ─────────────────────────────────────────────────────────────────

type Service struct {
	repo            *Repository
	hub             *websocket.Hub
	notifService    *notification.Service
	presenceService *presence.Service
}

func NewService(repo *Repository, hub *websocket.Hub, notif *notification.Service, pres *presence.Service) *Service {
	return &Service{
		repo:            repo,
		hub:             hub,
		notifService:    notif,
		presenceService: pres,
	}
}

// HandleIncomingMessage persists a message and delivers it.
// Called by the WebSocket hub when a MESSAGE envelope arrives.
func (s *Service) HandleIncomingMessage(ctx context.Context, msg *Message) error {
	msg.Status = "SENT"
	if err := s.repo.Save(ctx, msg); err != nil {
		return err
	}

	// Marshal full message as envelope payload
	payloadBytes, _ := json.Marshal(msg)

	// Try live delivery via WebSocket
	env := websocket.Envelope{
		Type:    websocket.TypeMessage,
		ID:      uuid.NewString(),
		From:    msg.SenderID,
		To:      msg.RecipientID,
		Payload: json.RawMessage(payloadBytes),
	}
	online := s.hub.SendToUser(msg.RecipientID, env)

	if !online {
		// Queue offline
		if err := s.repo.QueueOffline(ctx, msg.ID, msg.RecipientID); err != nil {
			log.Error().Err(err).Str("msgID", msg.ID).Msg("Failed to queue offline message")
		}
		// Send FCM push notification (metadata only — content stays encrypted)
		tokens, _ := s.repo.GetFCMTokens(ctx, msg.RecipientID)
		senderName := s.repo.GetDisplayName(ctx, msg.SenderID)
		for _, token := range tokens {
			s.notifService.SendMessageNotification(token, senderName, msg.RecipientID)
		}
	} else {
		// Mark as delivered immediately
		s.repo.MarkDelivered(ctx, msg.ID)
	}
	return nil
}

func (s *Service) GetConversation(ctx context.Context, userA, userB string, limit int, before time.Time) ([]Message, error) {
	return s.repo.GetConversation(ctx, userA, userB, limit, before)
}

func (s *Service) MarkDelivered(ctx context.Context, msgID string) error {
	return s.repo.MarkDelivered(ctx, msgID)
}

func (s *Service) MarkRead(ctx context.Context, msgID string) error {
	return s.repo.MarkRead(ctx, msgID)
}

func (s *Service) DeliverOfflineMessages(ctx context.Context, userID string) {
	msgs, err := s.repo.GetOfflineMessages(ctx, userID)
	if err != nil {
		log.Error().Err(err).Str("userID", userID).Msg("Failed to get offline messages")
		return
	}
	for _, msg := range msgs {
		payloadBytes, _ := json.Marshal(msg)
		env := websocket.Envelope{
			Type:    websocket.TypeMessage,
			ID:      uuid.NewString(),
			From:    msg.SenderID,
			To:      userID,
			Payload: json.RawMessage(payloadBytes),
		}
		s.hub.SendToUser(userID, env)
	}
}
