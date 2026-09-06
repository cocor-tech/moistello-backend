package response

import (
	"github.com/gin-gonic/gin"
	"net/http"
)

type Envelope struct {
	Success   bool   `json:"success"`
	Code      string `json:"code,omitempty"`
	Message   string `json:"message,omitempty"`
	Details   any    `json:"details,omitempty"`
	RequestId string `json:"requestId,omitempty"`
	Data      any    `json:"data,omitempty"`
	Meta      any    `json:"meta,omitempty"`
}

type PaginationMeta struct {
	Page       int  `json:"page"`
	Limit      int  `json:"limit"`
	TotalItems int  `json:"totalItems"`
	TotalPages int  `json:"totalPages"`
	Total      int  `json:"total"`
	HasMore    bool `json:"hasMore"`
}

func NewPaginationMeta(page, limit, total int) PaginationMeta {
	totalPages := 0
	if limit > 0 {
		totalPages = (total + limit - 1) / limit
	}
	return PaginationMeta{
		Page:       page,
		Limit:      limit,
		TotalItems: total,
		TotalPages: totalPages,
		Total:      total,
		HasMore:    limit > 0 && page*limit < total,
	}
}

func getRequestID(c *gin.Context) string {
	if rid := c.GetHeader("X-Request-Id"); rid != "" {
		return rid
	}
	if rid, exists := c.Get("request_id"); exists {
		if s, ok := rid.(string); ok {
			return s
		}
	}
	return ""
}

func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, Envelope{
		Success:   true,
		Data:      data,
		RequestId: getRequestID(c),
	})
}

func OKWithMeta(c *gin.Context, data any, meta any) {
	c.JSON(http.StatusOK, Envelope{
		Success:   true,
		Data:      data,
		Meta:      meta,
		RequestId: getRequestID(c),
	})
}

func Error(c *gin.Context, status int, code, message string, details any) {
	c.JSON(status, Envelope{
		Success:   false,
		Code:      code,
		Message:   message,
		Details:   details,
		RequestId: getRequestID(c),
	})
}

func BadRequest(c *gin.Context, message string) {
	Error(c, http.StatusBadRequest, "BAD_REQUEST", message, nil)
}

func ValidationError(c *gin.Context, message string, details any) {
	Error(c, http.StatusBadRequest, "VALIDATION_ERROR", message, details)
}

func Unauthorized(c *gin.Context, message string) {
	Error(c, http.StatusUnauthorized, "UNAUTHORIZED", message, nil)
}

func Forbidden(c *gin.Context, message string) {
	Error(c, http.StatusForbidden, "FORBIDDEN", message, nil)
}

func NotFound(c *gin.Context, message string) {
	Error(c, http.StatusNotFound, "NOT_FOUND", message, nil)
}

func Conflict(c *gin.Context, message string) {
	Error(c, http.StatusConflict, "CONFLICT", message, nil)
}

func InternalError(c *gin.Context, message string) {
	Error(c, http.StatusInternalServerError, "INTERNAL_ERROR", message, nil)
}

// ErrorWithCode writes an error envelope with an explicit HTTP status and code.
func ErrorWithCode(c *gin.Context, statusCode int, code, message string) {
	Error(c, statusCode, code, message, nil)
}

// Success responds with a 200 OK success envelope carrying data.
func Success(c *gin.Context, data any) {
	OK(c, data)
}

// Created responds with a 201 Created success envelope carrying data.
func Created(c *gin.Context, data any) {
	c.JSON(http.StatusCreated, Envelope{
		Success:   true,
		Data:      data,
		RequestId: getRequestID(c),
	})
}

// ValidationErrors responds with a 422 Unprocessable Entity error envelope.
func ValidationErrors(c *gin.Context, message string) {
	Error(c, http.StatusUnprocessableEntity, "VALIDATION_ERROR", message, nil)
}
