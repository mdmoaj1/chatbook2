package websocket

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	ws "github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
)

var upgrader = ws.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin: func(r *http.Request) bool {
		return true // Controlled by CORS middleware
	},
}

// MessageDeliverer is implemented by messaging.Service to avoid import cycles
type MessageDeliverer interface {
	DeliverOfflineMessages(ctx context.Context, userID string)
	MarkDelivered(ctx context.Context, msgID string) error
	MarkRead(ctx context.Context, msgID string) error
}

type Handler struct {
	hub      *Hub
	msgSvc   MessageDeliverer
}

func NewHandler(hub *Hub, msgService any, sigService any, presenceService any) *Handler {
	h := &Handler{hub: hub}
	if md, ok := msgService.(MessageDeliverer); ok {
		h.msgSvc = md
	}
	return h
}

func (h *Handler) HandleConnection(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Error().Err(err).Msg("Failed to upgrade websocket")
		return
	}

	uid := userID.(string)
	client := &Client{
		ID:     uuid.NewString(),
		UserID: uid,
		Hub:    h.hub,
		Conn:   conn,
		Send:   make(chan []byte, 512),
	}

	client.Hub.register <- client

	// Deliver any pending offline messages after connecting
	if h.msgSvc != nil {
		go h.msgSvc.DeliverOfflineMessages(context.Background(), uid)
	}

	// Handle ACKs inline: when hub receives ACK/READ_RECEIPT, update DB
	go client.writePump()
	go h.readPumpWithHandlers(client)
}

// readPumpWithHandlers extends readPump to process certain message types on the server side
func (h *Handler) readPumpWithHandlers(c *Client) {
	defer func() {
		c.Hub.unregister <- c
		c.Conn.Close()
	}()
	c.Conn.SetReadLimit(maxMessageSize)
	c.Conn.SetReadDeadline(timeNow().Add(pongWait))
	c.Conn.SetPongHandler(func(string) error {
		c.Conn.SetReadDeadline(timeNow().Add(pongWait))
		return nil
	})

	for {
		_, message, err := c.Conn.ReadMessage()
		if err != nil {
			if ws.IsUnexpectedCloseError(err, ws.CloseGoingAway, ws.CloseAbnormalClosure) {
				log.Error().Err(err).Str("userID", c.UserID).Msg("WS read error")
			}
			break
		}

		var env Envelope
		if err := json.Unmarshal(message, &env); err != nil {
			log.Warn().Err(err).Msg("Failed to unmarshal WS message")
			continue
		}

		// Server-side handling for select types
		if h.msgSvc != nil {
			switch env.Type {
			case TypeAck:
				var ack AckPayload
				if err := json.Unmarshal(env.Payload, &ack); err == nil {
					if ack.Status == "DELIVERED" {
						h.msgSvc.MarkDelivered(context.Background(), ack.MessageID)
					} else if ack.Status == "READ" {
						h.msgSvc.MarkRead(context.Background(), ack.MessageID)
					}
				}
				continue // ACKs are not relayed back
			case TypePing:
				// Send pong
				pong := Envelope{Type: TypePong, ID: uuid.NewString()}
				c.SendEnvelope(pong)
				continue
			}
		}

		// Route message to recipient or broadcast
		env.From = c.UserID
		if env.To != "" {
			c.Hub.SendToUser(env.To, env)
		} else {
			c.Hub.broadcast <- env
		}
	}
}
