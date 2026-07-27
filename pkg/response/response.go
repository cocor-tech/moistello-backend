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

// Pagination is the stable list-response contract. PaginationMeta remains in
// the envelope for clients using the original camelCase API.
type Pagination struct {
	Page       int `json:"page"`
	PageSize   int `json:"page_size"`
	Total      int `json:"total"`
	TotalPages int `json:"total_pages"`
}

type Envelope = APIResponse

type APIResponse struct {
	Success    bool            `json:"success"`
	Data       any             `json:"data,omitempty"`
	Error      string          `json:"error,omitempty"`
	Code       string          `json:"code,omitempty"`
	StatusCode int             `json:"statusCode,omitempty"`
	RequestID  string          `json:"requestId,omitempty"`
	Meta       *PaginationMeta `json:"meta,omitempty"`
	Pagination *Pagination     `json:"pagination,omitempty"`
}

func getReqID(c *gin.Context) string {
	if reqID, exists := c.Get("requestID"); exists {
		if s, ok := reqID.(string); ok {
			return s
		}
	}
	return c.GetHeader("X-Request-ID")
}

func ErrorWithCode(c *gin.Context, statusCode int, code, msg string) {
	c.JSON(statusCode, APIResponse{
		Success:    false,
		Error:      msg,
		Code:       code,
		StatusCode: statusCode,
		RequestID:  getReqID(c),
	})
}

func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, APIResponse{Success: true, Data: data})
}

func OKWithMeta(c *gin.Context, data any, meta *PaginationMeta) {
	var current *Pagination
	if meta != nil {
		current = &Pagination{
			Page: meta.Page, PageSize: meta.Limit, Total: meta.Total, TotalPages: meta.TotalPages,
		}
	}
	c.JSON(http.StatusOK, APIResponse{
		Success: true, Data: data, Meta: meta, Pagination: current,
	})
}

func Created(c *gin.Context, data any) {
	c.JSON(http.StatusCreated, APIResponse{Success: true, Data: data})
}

func BadRequest(c *gin.Context, msg string) {
	ErrorWithCode(c, http.StatusBadRequest, "BAD_REQUEST", msg)
}

func Unauthorized(c *gin.Context, msg string) {
	ErrorWithCode(c, http.StatusUnauthorized, "UNAUTHORIZED", msg)
}

func Forbidden(c *gin.Context, msg string) {
	ErrorWithCode(c, http.StatusForbidden, "FORBIDDEN", msg)
}

func NotFound(c *gin.Context, msg string) {
	ErrorWithCode(c, http.StatusNotFound, "NOT_FOUND", msg)
}

func Conflict(c *gin.Context, msg string) {
	ErrorWithCode(c, http.StatusConflict, "CONFLICT", msg)
}

func InternalError(c *gin.Context, msg string) {
	ErrorWithCode(c, http.StatusInternalServerError, "INTERNAL_ERROR", msg)
}

func ValidationErrors(c *gin.Context, msg string) {
	ErrorWithCode(c, http.StatusUnprocessableEntity, "VALIDATION_ERROR", msg)
}

func NewPaginationMeta(page, limit, total int) *PaginationMeta {
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 20
	}
	totalPages := (total + limit - 1) / limit
	return &PaginationMeta{Page: page, Limit: limit, Total: total, TotalPages: totalPages}
}
