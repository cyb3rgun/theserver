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

const deviceTokenKey contextKey = 2

// DeviceFrom returns the device a package download is authorized as.
func DeviceFrom(ctx context.Context) (store.Device, bool) {
	device, ok := ctx.Value(deviceTokenKey).(store.Device)
	return device, ok
}

// authorizeDownload lets a package download through with the token of an
// approved device or with an admin token (D-038): 401 without a token or
// with one nobody holds, 403 for a device that is not approved or a revoked
// admin token.
func (s *Server) authorizeDownload(next http.Handler) http.Handler {
	admin := s.authorize(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		bearer, found := bearerToken(r)
		if _, inProcess := ctx.Value(adminKey).(string); inProcess || !found {
			admin.ServeHTTP(w, r)
			return
		}
		device, err := s.opts.Store.DeviceByToken(ctx, bearer)
		switch {
		case errors.Is(err, store.ErrDeviceNotFound):
			admin.ServeHTTP(w, r)
		case err != nil:
			s.fail(w, r, err)
		case device.Status != store.StatusApproved:
			writeError(w, http.StatusForbidden, codeForbidden, "device "+device.ID+" is not approved")
		default:
			next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, deviceTokenKey, device)))
		}
	})
}

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
