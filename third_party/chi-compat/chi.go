// Package chi forwards the legacy imports used by github.com/stellar/go to chi v5.
package chi

import (
	"context"

	chi "github.com/go-chi/chi/v5"
)

type Mux = chi.Mux

func NewMux() *Mux {
	return chi.NewMux()
}

func NewRouteContext() *chi.Context {
	return chi.NewRouteContext()
}

func RouteContext(ctx context.Context) *chi.Context {
	return chi.RouteContext(ctx)
}
