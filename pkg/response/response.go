package response

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type PaginationMeta struct {
	Page       int `json:"page"`
	Limit      int `json:"limit"`
	Total      int `json:"total"`
	TotalPages int `json:"totalPages"`
}

type Envelope = APIResponse

type APIResponse struct {
	Success bool            `json:"success"`
	Data    any             `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
	Meta    *PaginationMeta `json:"meta,omitempty"`
}

func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, APIResponse{Success: true, Data: data})
}

func OKWithMeta(c *gin.Context, data any, meta *PaginationMeta) {
	c.JSON(http.StatusOK, APIResponse{Success: true, Data: data, Meta: meta})
}

func Created(c *gin.Context, data any) {
	c.JSON(http.StatusCreated, APIResponse{Success: true, Data: data})
}

func BadRequest(c *gin.Context, msg string) {
	c.JSON(http.StatusBadRequest, APIResponse{Success: false, Error: msg})
}

func Unauthorized(c *gin.Context, msg string) {
	c.JSON(http.StatusUnauthorized, APIResponse{Success: false, Error: msg})
}

func Forbidden(c *gin.Context, msg string) {
	c.JSON(http.StatusForbidden, APIResponse{Success: false, Error: msg})
}

func NotFound(c *gin.Context, msg string) {
	c.JSON(http.StatusNotFound, APIResponse{Success: false, Error: msg})
}

func Conflict(c *gin.Context, msg string) {
	c.JSON(http.StatusConflict, APIResponse{Success: false, Error: msg})
}

func InternalError(c *gin.Context, msg string) {
	c.JSON(http.StatusInternalServerError, APIResponse{Success: false, Error: msg})
}

func ValidationErrors(c *gin.Context, msg string) {
	c.JSON(http.StatusUnprocessableEntity, APIResponse{Success: false, Error: msg})
}

func NewPaginationMeta(page, limit, total int) *PaginationMeta {
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 20
	}
	totalPages := (total + limit - 1) / limit
	if totalPages < 1 {
		totalPages = 1
	}
	return &PaginationMeta{Page: page, Limit: limit, Total: total, TotalPages: totalPages}
}
