package contact

import (
	"database/sql"
	"github.com/gin-gonic/gin"
)

type Repository struct{}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{}
}

type Service struct{}

func NewService(repo *Repository) *Service {
	return &Service{}
}

type Handler struct{}

func NewHandler(svc *Service) *Handler {
	return &Handler{}
}

func (h *Handler) List(c *gin.Context)    {}
func (h *Handler) Add(c *gin.Context)     {}
func (h *Handler) Remove(c *gin.Context)  {}
func (h *Handler) Block(c *gin.Context)   {}
func (h *Handler) Unblock(c *gin.Context) {}
