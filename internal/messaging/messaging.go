package messaging

import (
	"database/sql"

	"github.com/chatbook/backend/internal/notification"
	"github.com/chatbook/backend/internal/presence"
	"github.com/chatbook/backend/internal/websocket"
)

type Repository struct{}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{}
}

type Service struct{}

func NewService(repo *Repository, hub *websocket.Hub, notif *notification.Service, pres *presence.Service) *Service {
	return &Service{}
}
