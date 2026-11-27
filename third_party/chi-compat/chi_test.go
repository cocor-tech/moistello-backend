package chi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
)

func TestStellarRoutingCompatibility(t *testing.T) {
	mux := chi.NewMux()
	mux.Use(middleware.RequestID, middleware.Recoverer)
	mux.Get("/accounts/{id}", func(w http.ResponseWriter, r *http.Request) {
		if got := chi.RouteContext(r.Context()).URLParam("id"); got != "123" {
			t.Errorf("route parameter = %q, want 123", got)
		}
		if middleware.GetReqID(r.Context()) == "" {
			t.Error("request ID is empty")
		}
		w.WriteHeader(http.StatusNoContent)
	})

	for _, test := range []struct {
		path string
		want int
	}{
		{"/accounts/123", http.StatusNoContent},
		{"/unknown", http.StatusNotFound},
	} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
		if response.Code != test.want {
			t.Errorf("GET %s: status = %d, want %d", test.path, response.Code, test.want)
		}
	}
}
