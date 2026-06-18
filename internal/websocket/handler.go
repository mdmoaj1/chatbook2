package websocket

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	ws "github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
)

var upgrader = ws.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for now
	},
}

type Handler struct {
	hub *Hub
}

// NewHandler accepts any to avoid import cycles with services that import websocket
func NewHandler(hub *Hub, msgService any, sigService any, presenceService any) *Handler {
	return &Handler{hub: hub}
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

	client := &Client{
		ID:     uuid.NewString(),
		UserID: userID.(string),
		Hub:    h.hub,
		Conn:   conn,
		Send:   make(chan []byte, 256),
	}

	client.Hub.register <- client

	// Start pump goroutines
	go client.writePump()
	go client.readPump()
}
