package response

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/moistello/backend/pkg/apperrors"
)

// Envelope represents both the legacy Moistello API response wrapper and
// RFC 9457 Problem Details object for error responses.
type Envelope struct {
	Success   bool   `json:"success"`
	Code      string `json:"code,omitempty"`
	Message   string `json:"message,omitempty"`
	Details   any    `json:"details,omitempty"`
	RequestId string `json:"requestId,omitempty"`
	Data      any    `json:"data,omitempty"`
	Meta      any    `json:"meta,omitempty"`

	// RFC 9457 Problem Details fields
	Type     string `json:"type,omitempty"`
	Title    string `json:"title,omitempty"`
	Status   int    `json:"status,omitempty"`
	Detail   string `json:"detail,omitempty"`
	Instance string `json:"instance,omitempty"`
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
	if c == nil {
		return ""
	}
	if rid := c.GetHeader("X-Request-ID"); rid != "" {
		return rid
	}
	if rid := c.GetHeader("X-Request-Id"); rid != "" {
		return rid
	}
	if rid, exists := c.Get("requestID"); exists {
		if s, ok := rid.(string); ok && s != "" {
			return s
		}
	}
	if rid, exists := c.Get("request_id"); exists {
		if s, ok := rid.(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func getInstanceURI(c *gin.Context) string {
	if c != nil && c.Request != nil && c.Request.URL != nil {
		return c.Request.URL.Path
	}
	return ""
}

func statusTitle(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "Bad Request"
	case http.StatusUnauthorized:
		return "Unauthorized"
	case http.StatusForbidden:
		return "Forbidden"
	case http.StatusNotFound:
		return "Not Found"
	case http.StatusConflict:
		return "Conflict"
	case http.StatusUnprocessableEntity:
		return "Unprocessable Entity"
	case http.StatusTooManyRequests:
		return "Too Many Requests"
	case http.StatusRequestEntityTooLarge:
		return "Request Entity Too Large"
	case http.StatusInternalServerError:
		return "Internal Server Error"
	case http.StatusBadGateway:
		return "Bad Gateway"
	case http.StatusServiceUnavailable:
		return "Service Unavailable"
	default:
		if txt := http.StatusText(status); txt != "" {
			return txt
		}
		return "Error"
	}
}

func problemTypeURI(code string) string {
	if code == "" {
		return "about:blank"
	}
	slug := strings.ToLower(strings.ReplaceAll(code, "_", "-"))
	return fmt.Sprintf("https://moistello.com/probs/%s", slug)
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

// Error writes an RFC 9457 compliant problem details error response while
// maintaining backward compatibility with legacy Envelope fields.
func Error(c *gin.Context, status int, code, message string, details any) {
	if code == "" {
		code = "ERROR"
	}
	c.JSON(status, Envelope{
		Success:   false,
		Code:      code,
		Message:   message,
		Details:   details,
		RequestId: getRequestID(c),
		Status:    status,
		Title:     statusTitle(status),
		Detail:    message,
		Type:      problemTypeURI(code),
		Instance:  getInstanceURI(c),
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

// Specific 4xx RFC 9457 helper constructors

func InvalidCredentials(c *gin.Context, message string) {
	Error(c, http.StatusUnauthorized, "INVALID_CREDENTIALS", message, nil)
}

func TokenExpired(c *gin.Context, message string) {
	Error(c, http.StatusUnauthorized, "TOKEN_EXPIRED", message, nil)
}

func NonceExpired(c *gin.Context, message string) {
	Error(c, http.StatusUnauthorized, "NONCE_EXPIRED", message, nil)
}

func RateLimitExceeded(c *gin.Context, message string) {
	Error(c, http.StatusTooManyRequests, "RATE_LIMIT_EXCEEDED", message, nil)
}

// RequestEntityTooLarge responds with 413 Request Entity Too Large, reporting
// the cap the request actually violated so the client knows whether to shrink
// the payload or split it across calls (#445).
func RequestEntityTooLarge(c *gin.Context, limit int64) {
	Error(c, http.StatusRequestEntityTooLarge, "REQUEST_BODY_TOO_LARGE",
		fmt.Sprintf("%s (limit %d bytes)", apperrors.ErrRequestEntityTooLarge, limit),
		gin.H{"limitBytes": limit})
}
