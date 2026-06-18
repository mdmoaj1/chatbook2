package websocket

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/chatbook/backend/internal/presence"
	"github.com/google/uuid"
	ws "github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
)

// ─── Message Envelope ────────────────────────────────────────────────────────
// ALL WebSocket messages use this typed envelope.
// Backend NEVER sends audio, video, or file bytes through here.
// Only: text messages, signaling (SDP/ICE), presence, typing, ACKs, notifications.

type MessageType string

const (
	TypeMessage             MessageType = "MESSAGE"
	TypeAck                 MessageType = "ACK"
	TypeReadReceipt         MessageType = "READ_RECEIPT"
	TypeTypingStart         MessageType = "TYPING_START"
	TypeTypingStop          MessageType = "TYPING_STOP"
	TypePresence            MessageType = "PRESENCE"
	TypeSignalOffer         MessageType = "SIGNAL_OFFER"
	TypeSignalAnswer        MessageType = "SIGNAL_ANSWER"
	TypeSignalIce           MessageType = "SIGNAL_ICE"
	TypeCallInvite          MessageType = "CALL_INVITE"
	TypeCallAccept          MessageType = "CALL_ACCEPT"
	TypeCallReject          MessageType = "CALL_REJECT"
	TypeCallEnd             MessageType = "CALL_END"
	TypeFileTransferNotify  MessageType = "FILE_TRANSFER_NOTIFY"  // Metadata only — no file bytes
	TypeFileTransferReady   MessageType = "FILE_TRANSFER_READY"
	TypeGroupCallInvite     MessageType = "GROUP_CALL_INVITE"
	TypeGroupCallJoin       MessageType = "GROUP_CALL_JOIN"
	TypeGroupCallLeave      MessageType = "GROUP_CALL_LEAVE"
	TypePing                MessageType = "PING"
	TypePong                MessageType = "PONG"
)

type Envelope struct {
	Type      MessageType     `json:"type"`
	ID        string          `json:"id"`
	From      string          `json:"from"`
	To        string          `json:"to"`           // Empty for broadcasts
	GroupID   string          `json:"group_id,omitempty"`
	Payload   json.RawMessage `json:"payload"`
	Timestamp int64           `json:"timestamp"`
}

// ─── Payload Types ────────────────────────────────────────────────────────────

type MessagePayload struct {
	MessageID        string `json:"message_id"`
	ContentEncrypted string `json:"content_encrypted"` // AES-GCM ciphertext
	ContentIV        string `json:"content_iv"`         // AES-GCM IV (Base64)
	DHPublicKey      string `json:"dh_public_key"`      // Signal ratchet DH key
	MessageIndex     int    `json:"message_index"`
	MessageType      string `json:"message_type"`
}

type AckPayload struct {
	MessageID   string `json:"message_id"`
	Status      string `json:"status"` // "DELIVERED" | "READ"
	DeliveredAt int64  `json:"delivered_at,omitempty"`
	ReadAt      int64  `json:"read_at,omitempty"`
}

type TypingPayload struct {
	ConversationID string `json:"conversation_id"`
}

type PresencePayload struct {
	UserID   string `json:"user_id"`
	Status   string `json:"status"`   // "ONLINE" | "OFFLINE"
	LastSeen int64  `json:"last_seen"`
}

type SignalPayload struct {
	CallID    string `json:"call_id"`
	SDP       string `json:"sdp,omitempty"`       // SDP offer or answer
	Candidate string `json:"candidate,omitempty"` // ICE candidate JSON
	SdpMid    string `json:"sdp_mid,omitempty"`
	SdpMLine  int    `json:"sdp_m_line,omitempty"`
}

type CallPayload struct {
	CallID       string   `json:"call_id"`
	CallType     string   `json:"call_type"` // "audio" | "video" | "group_audio" | "group_video"
	GroupID      string   `json:"group_id,omitempty"`
	Participants []string `json:"participants,omitempty"` // For group calls
}

// FileTransferNotifyPayload — metadata only, NEVER contains file bytes
type FileTransferNotifyPayload struct {
	TransferID  string `json:"transfer_id"`
	FileName    string `json:"file_name"`
	FileSize    int64  `json:"file_size"`
	MimeType    string `json:"mime_type"`
	SHA256Hash  string `json:"sha256_hash"`
	ChunkSize   int    `json:"chunk_size"`
	TotalChunks int    `json:"total_chunks"`
}

// ─── Client ───────────────────────────────────────────────────────────────────

type Client struct {
	ID     string
	UserID string
	Hub    *Hub
	Conn   *ws.Conn
	Send   chan []byte
	mu     sync.Mutex
}

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 512 * 1024
)

func (c *Client) readPump() {
	defer func() {
		c.Hub.unregister <- c
		c.Conn.Close()
	}()
	c.Conn.SetReadLimit(maxMessageSize)
	c.Conn.SetReadDeadline(time.Now().Add(pongWait))
	c.Conn.SetPongHandler(func(string) error { c.Conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	for {
		_, message, err := c.Conn.ReadMessage()
		if err != nil {
			if ws.IsUnexpectedCloseError(err, ws.CloseGoingAway, ws.CloseAbnormalClosure) {
				log.Error().Err(err).Msg("error reading websocket message")
			}
			break
		}
		var env Envelope
		if err := json.Unmarshal(message, &env); err != nil {
			log.Warn().Err(err).Msg("failed to unmarshal message")
			continue
		}
		// Route message via Hub
		if env.To != "" {
			c.Hub.SendToUser(env.To, env)
		} else {
			c.Hub.broadcast <- env
		}
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.Conn.Close()
	}()
	for {
		select {
		case message, ok := <-c.Send:
			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// The hub closed the channel.
				c.Conn.WriteMessage(ws.CloseMessage, []byte{})
				return
			}

			w, err := c.Conn.NextWriter(ws.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			if err := w.Close(); err != nil {
				return
			}
		case <-ticker.C:
			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.Conn.WriteMessage(ws.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (c *Client) SendEnvelope(env Envelope) error {
	env.Timestamp = time.Now().Unix()
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	select {
	case c.Send <- data:
	default:
		log.Warn().Str("userID", c.UserID).Msg("Client send buffer full, dropping message")
	}
	return nil
}

// ─── Hub ─────────────────────────────────────────────────────────────────────

type Hub struct {
	clients    map[string]*Client // userID → client
	mu         sync.RWMutex
	register   chan *Client
	unregister chan *Client
	broadcast  chan Envelope
	direct     chan DirectMessage

	presenceService *presence.Service
}

type DirectMessage struct {
	ToUserID string
	Envelope Envelope
}

func NewHub(presenceSvc *presence.Service) *Hub {
	return &Hub{
		clients:         make(map[string]*Client),
		register:        make(chan *Client, 256),
		unregister:      make(chan *Client, 256),
		broadcast:       make(chan Envelope, 512),
		direct:          make(chan DirectMessage, 1024),
		presenceService: presenceSvc,
	}
}

func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client.UserID] = client
			h.mu.Unlock()
			h.presenceService.SetOnline(client.UserID)
			h.broadcastPresence(client.UserID, "ONLINE")
			log.Info().Str("userID", client.UserID).Msg("WS: client connected")

		case client := <-h.unregister:
			h.mu.Lock()
			if existing, ok := h.clients[client.UserID]; ok && existing.ID == client.ID {
				delete(h.clients, client.UserID)
				close(client.Send)
			}
			h.mu.Unlock()
			h.presenceService.SetOffline(client.UserID)
			h.broadcastPresence(client.UserID, "OFFLINE")
			log.Info().Str("userID", client.UserID).Msg("WS: client disconnected")

		case dm := <-h.direct:
			h.mu.RLock()
			target, ok := h.clients[dm.ToUserID]
			h.mu.RUnlock()
			if ok {
				target.SendEnvelope(dm.Envelope)
			}

		case env := <-h.broadcast:
			h.mu.RLock()
			for _, client := range h.clients {
				client.SendEnvelope(env)
			}
			h.mu.RUnlock()
		}
	}
}

// SendToUser delivers a message to a specific user if online.
// Returns false if user is offline (caller should push FCM notification).
func (h *Hub) SendToUser(userID string, env Envelope) bool {
	h.direct <- DirectMessage{ToUserID: userID, Envelope: env}
	h.mu.RLock()
	_, online := h.clients[userID]
	h.mu.RUnlock()
	return online
}

// SendToGroup sends to all group participants.
// Each message still flows through the backend only as signaling —
// actual audio/video travels peer-to-peer.
func (h *Hub) SendToGroup(participantIDs []string, env Envelope, excludeUserID string) {
	for _, uid := range participantIDs {
		if uid == excludeUserID {
			continue
		}
		h.direct <- DirectMessage{ToUserID: uid, Envelope: env}
	}
}

// IsOnline checks if user has an active WebSocket connection on THIS pod.
// For multi-pod setups, use Redis presence instead.
func (h *Hub) IsOnline(userID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.clients[userID]
	return ok
}

func (h *Hub) broadcastPresence(userID, status string) {
	payload, _ := json.Marshal(PresencePayload{
		UserID:   userID,
		Status:   status,
		LastSeen: time.Now().Unix(),
	})
	h.broadcast <- Envelope{
		Type:      TypePresence,
		ID:        uuid.NewString(),
		Payload:   payload,
		Timestamp: time.Now().Unix(),
	}
}
