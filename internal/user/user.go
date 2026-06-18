package user

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

func (h *Handler) GetMe(c *gin.Context)            {}
func (h *Handler) UpdateProfile(c *gin.Context)    {}
func (h *Handler) UpdateFCMToken(c *gin.Context)   {}
func (h *Handler) Search(c *gin.Context)           {}
