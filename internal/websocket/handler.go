package websocket

import "github.com/gin-gonic/gin"

type Handler struct{}

// NewHandler accepts any to avoid import cycles with services that import websocket
func NewHandler(hub *Hub, msgService any, sigService any, presenceService any) *Handler {
	return &Handler{}
}

func (h *Handler) HandleConnection(c *gin.Context) {}
