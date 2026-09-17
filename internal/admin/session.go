package admin

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cyb3rgun/theserver/internal/i18n"
	"github.com/cyb3rgun/theserver/internal/store"
)

// CookieName is the name of the admin session cookie.
const CookieName = "theserver_admin"

// SessionLifetime is how long a login lasts unless Options.SessionLifetime
// says otherwise; it is the default of admin.session_hours.
const SessionLifetime = 12 * time.Hour

const (
	minKeyLength = 32
	cookiePath   = "/admin"
	cookieFormat = "v1"
)

// KeyFileName is the file that holds the cookie key in the data directory.
const KeyFileName = "admin.key"

// LoadOrCreateKey reads the cookie signing key from path, or creates one of
// 32 random bytes with mode 0600. Keeping it in the data directory lets a
// login survive a restart; deleting it logs everybody out.
func LoadOrCreateKey(path string) ([]byte, error) {
	key, err := os.ReadFile(path)
	if err == nil {
		if len(key) < minKeyLength {
			return nil, fmt.Errorf("admin key %s has %d bytes, needs %d", path, len(key), minKeyLength)
		}
		return key, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	key = make([]byte, minKeyLength)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create admin key: %w", err)
	}
	if _, err := f.Write(key); err != nil {
		f.Close()
		os.Remove(path)
		return nil, err
	}
	return key, f.Close()
}

// session is a logged in admin and the language of the request.
type session struct {
	TokenID string
	Name    string
	Lang    string
}

// NewSessionCookie signs a session for an admin token id, valid for
// SessionLifetime from now. The cookie carries the id and the expiry, never
// the token.
func NewSessionCookie(key []byte, tokenID string, now time.Time) *http.Cookie {
	return newSessionCookie(key, tokenID, now, SessionLifetime)
}

func newSessionCookie(key []byte, tokenID string, now time.Time, lifetime time.Duration) *http.Cookie {
	expires := now.Add(lifetime)
	payload := strings.Join([]string{cookieFormat, tokenID, strconv.FormatInt(expires.Unix(), 10)}, "|")
	value := base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." +
		base64.RawURLEncoding.EncodeToString(sign(key, payload))
	return &http.Cookie{
		Name:     CookieName,
		Value:    value,
		Path:     cookiePath,
		Expires:  expires,
		MaxAge:   int(lifetime.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	}
}

func expiredCookie() *http.Cookie {
	return &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     cookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	}
}

func sign(key []byte, payload string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payload))
	return mac.Sum(nil)
}

// tokenIDFromCookie checks signature and expiry and returns the token id.
func tokenIDFromCookie(key []byte, value string, now time.Time) (string, bool) {
	encoded, signature, found := strings.Cut(value, ".")
	if !found {
		return "", false
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", false
	}
	mac, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil || !hmac.Equal(mac, sign(key, string(payload))) {
		return "", false
	}
	parts := strings.Split(string(payload), "|")
	if len(parts) != 3 || parts[0] != cookieFormat || parts[1] == "" {
		return "", false
	}
	expires, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || now.Unix() >= expires {
		return "", false
	}
	return parts[1], true
}

// authenticate finds the session of a request: a valid cookie whose admin
// token still exists and is not revoked.
func (a *Admin) authenticate(r *http.Request) (session, bool) {
	cookie, err := r.Cookie(CookieName)
	if err != nil {
		return session{}, false
	}
	id, ok := tokenIDFromCookie(a.opts.Key, cookie.Value, a.opts.Now())
	if !ok {
		return session{}, false
	}
	token, err := a.opts.Store.GetAdminToken(r.Context(), id)
	if err != nil || token.Revoked() {
		return session{}, false
	}
	return session{TokenID: token.ID, Name: token.Name}, true
}

// sessionLifetime is how long a new login lasts (admin.session_hours).
func (a *Admin) sessionLifetime() time.Duration {
	if a.opts.SessionLifetime != nil {
		if d := a.opts.SessionLifetime(); d > 0 {
			return d
		}
	}
	return SessionLifetime
}

// sessionEnded sends a browser without a valid session to the login page. An
// HTMX request is told to go there with HX-Redirect.
func (a *Admin) sessionEnded(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, expiredCookie())
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/admin/login")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

// page wraps a page or HTMX handler with the session check.
func (a *Admin) page(h func(http.ResponseWriter, *http.Request, session)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, ok := a.authenticate(r)
		if !ok {
			a.sessionEnded(w, r)
			return
		}
		s.Lang = a.lang(r)
		h(w, r, s)
	})
}

type loginData struct {
	layout
}

func (a *Admin) loginPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticate(r); ok {
		http.Redirect(w, r, "/admin/devices", http.StatusSeeOther)
		return
	}
	lang := a.lang(r)
	a.render(w, lang, http.StatusOK, "login", "layout", loginData{layout: a.layout("admin.login.title", "", session{Lang: lang})})
}

// login checks the pasted admin token once and sets the session cookie.
func (a *Admin) login(w http.ResponseWriter, r *http.Request) {
	lang := a.lang(r)
	data := loginData{layout: a.layout("admin.login.title", "", session{Lang: lang})}
	token := strings.TrimSpace(r.PostFormValue("token"))
	row, err := a.opts.Store.VerifyAdminToken(r.Context(), token)
	switch {
	case errors.Is(err, store.ErrAdminTokenUnknown):
		data.Error = i18n.T(lang, "admin.login.invalid")
		a.render(w, lang, http.StatusUnauthorized, "login", "layout", data)
		return
	case errors.Is(err, store.ErrAdminTokenRevoked):
		data.Error = i18n.T(lang, "admin.login.revoked")
		a.render(w, lang, http.StatusForbidden, "login", "layout", data)
		return
	case err != nil:
		a.log.Error("admin login failed", "error", err)
		data.Error = i18n.T(lang, "admin.login.failed")
		a.render(w, lang, http.StatusInternalServerError, "login", "layout", data)
		return
	}
	http.SetCookie(w, newSessionCookie(a.opts.Key, row.ID, a.opts.Now(), a.sessionLifetime()))
	a.log.Info("admin logged in", "admin_token", row.ID, "admin_name", row.Name, "remote", r.RemoteAddr)
	http.Redirect(w, r, "/admin/devices", http.StatusSeeOther)
}

func (a *Admin) logout(w http.ResponseWriter, r *http.Request) {
	if s, ok := a.authenticate(r); ok {
		a.log.Info("admin logged out", "admin_token", s.TokenID, "admin_name", s.Name)
	}
	http.SetCookie(w, expiredCookie())
	http.SetCookie(w, expiredLanguageCookie())
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}
