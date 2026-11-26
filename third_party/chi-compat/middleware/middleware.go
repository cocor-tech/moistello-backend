// Package middleware forwards the chi middleware used by github.com/stellar/go.
package middleware

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
)

func GetReqID(ctx context.Context) string {
	return middleware.GetReqID(ctx)
}

func Recoverer(next http.Handler) http.Handler {
	return middleware.Recoverer(next)
}

func RequestID(next http.Handler) http.Handler {
	return middleware.RequestID(next)
}
