package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cyb3rgun/theserver/internal/link"
	"github.com/cyb3rgun/theserver/internal/store"
	"github.com/cyb3rgun/theserver/internal/version"
)

type fakeDB struct {
	err error
}

func (f fakeDB) Ping(context.Context) error {
	return f.err
}

func quiet() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func getHealth(t *testing.T, handler http.Handler) (int, Health, http.Header) {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	var body Health
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q is not JSON: %v", rec.Body.String(), err)
	}
	return rec.Code, body, rec.Header()
}

func TestHealthzOK(t *testing.T) {
	code, body, header := getHealth(t, New(fakeDB{}, nil, quiet()))
	if code != http.StatusOK {
		t.Errorf("status %d, want 200", code)
	}
	want := Health{Status: "ok", Version: version.Version, DB: "ok"}
	if body != want {
		t.Errorf("body %+v, want %+v", body, want)
	}
	if header.Get("Content-Type") != "application/json" || header.Get("Cache-Control") != "no-store" {
		t.Errorf("headers are %v", header)
	}
}

func TestHealthzDatabaseDown(t *testing.T) {
	code, body, _ := getHealth(t, New(fakeDB{err: errors.New("disk gone")}, nil, quiet()))
	if code != http.StatusServiceUnavailable {
		t.Errorf("status %d, want 503", code)
	}
	want := Health{Status: "error", Version: version.Version, DB: "error"}
	if body != want {
		t.Errorf("body %+v, want %+v", body, want)
	}
}

// TestHealthzClosedStore uses the real store: once it is closed, the health
// endpoint reports the database as down.
func TestHealthzClosedStore(t *testing.T) {
	st, err := store.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler := New(st, nil, quiet())
	if code, _, _ := getHealth(t, handler); code != http.StatusOK {
		t.Fatalf("status %d with an open store, want 200", code)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if code, body, _ := getHealth(t, handler); code != http.StatusServiceUnavailable || body.DB != "error" {
		t.Errorf("status %d, db %q with a closed store, want 503 and error", code, body.DB)
	}
}

func TestRoutes(t *testing.T) {
	var linkCalls int
	deviceLink := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		linkCalls++
		w.WriteHeader(http.StatusTeapot)
	})
	handler := New(fakeDB{}, deviceLink, quiet())

	tests := []struct {
		method string
		path   string
		want   int
	}{
		{http.MethodGet, link.Path, http.StatusTeapot},
		{http.MethodPost, link.Path, http.StatusMethodNotAllowed},
		{http.MethodPost, "/healthz", http.StatusMethodNotAllowed},
		{http.MethodHead, "/healthz", http.StatusOK},
		{http.MethodGet, "/nothing", http.StatusNotFound},
	}
	for _, tt := range tests {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, nil))
		if rec.Code != tt.want {
			t.Errorf("%s %s answered %d, want %d", tt.method, tt.path, rec.Code, tt.want)
		}
	}
	if linkCalls != 1 {
		t.Errorf("the link handler was called %d times, want 1", linkCalls)
	}

	rec := httptest.NewRecorder()
	New(fakeDB{}, nil, quiet()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, link.Path, nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("without a link handler %s answered %d, want 404", link.Path, rec.Code)
	}
}
