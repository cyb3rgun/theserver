package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/cyb3rgun/theserver/internal/store"
)

type contextKey int

const adminKey contextKey = 0

// AsAdmin returns a context that authorizes an in process API request as the
// admin token with this id. The admin pages use it after they checked their
// session cookie; a request from the network cannot carry it. The token must
// still exist and must not be revoked.
func AsAdmin(ctx context.Context, tokenID string) context.Context {
	return context.WithValue(ctx, adminKey, tokenID)
}

// AdminFrom returns the admin token a request is authorized as.
func AdminFrom(ctx context.Context) (store.AdminToken, bool) {
	token, ok := ctx.Value(adminTokenKey).(store.AdminToken)
	return token, ok
}

const adminTokenKey contextKey = 1

// authorize lets a request through with a valid admin token (D-025): 401
// without one or with an unknown one, 403 with a revoked one.
func (s *Server) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		var (
			token store.AdminToken
			err   error
		)
		if id, ok := ctx.Value(adminKey).(string); ok {
			token, err = s.opts.Store.GetAdminToken(ctx, id)
			switch {
			case errors.Is(err, store.ErrAdminTokenNotFound):
				err = store.ErrAdminTokenUnknown
			case err == nil && token.Revoked():
				err = store.ErrAdminTokenRevoked
			}
		} else {
			bearer, found := bearerToken(r)
			if !found {
				w.Header().Set("WWW-Authenticate", `Bearer realm="theserver api"`)
				writeError(w, http.StatusUnauthorized, codeUnauthorized, "an admin token is required")
				return
			}
			token, err = s.opts.Store.VerifyAdminToken(ctx, bearer)
		}

		switch {
		case errors.Is(err, store.ErrAdminTokenUnknown):
			w.Header().Set("WWW-Authenticate", `Bearer realm="theserver api", error="invalid_token"`)
			writeError(w, http.StatusUnauthorized, codeUnauthorized, "unknown admin token")
			return
		case errors.Is(err, store.ErrAdminTokenRevoked):
			writeError(w, http.StatusForbidden, codeForbidden, "this admin token was revoked")
			return
		case err != nil:
			s.fail(w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, adminTokenKey, token)))
	})
}

func bearerToken(r *http.Request) (string, bool) {
	scheme, token, found := strings.Cut(r.Header.Get("Authorization"), " ")
	token = strings.TrimSpace(token)
	if !found || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return "", false
	}
	return token, true
}
