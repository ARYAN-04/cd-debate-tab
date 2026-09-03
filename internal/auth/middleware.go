package auth

import (
	"context"
	"crypto/subtle"
	"net/http"
	"time"

	"cd-debate-tab/internal/store"
)

// Cookie and header names for session auth and CSRF.
const (
	SessionCookie = "session"
	CSRFCookie    = "csrf"
	CSRFHeader    = "X-CSRF-Token"
)

type ctxKey string

// adminIDKey carries the authenticated admin ID through the request context.
const adminIDKey ctxKey = "adminID"

// AdminID returns the authenticated admin ID stored by RequireAuth.
func AdminID(r *http.Request) (string, bool) {
	id, ok := r.Context().Value(adminIDKey).(string)
	return id, ok
}

// SetSessionCookie issues the HttpOnly session cookie (PLAN §5).
func SetSessionCookie(w http.ResponseWriter, token string, secure bool, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  expires,
	})
}

// ClearSessionCookie removes the session cookie on logout.
func ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// RequireAuth redirects unauthenticated requests to /login with 302.
// A request is authenticated when the session cookie maps to a live,
// unexpired row in the sessions table.
func RequireAuth(s *store.Store, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(SessionCookie)
		if err != nil || c.Value == "" {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		sess, err := s.GetSessionByToken(r.Context(), c.Value)
		if err != nil || Expired(sess.ExpiresAt, time.Now()) {
			ClearSessionCookie(w)
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		ctx := context.WithValue(r.Context(), adminIDKey, sess.AdminID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

const csrfTokenKey ctxKey = "csrfToken"

// CSRFToken returns the current CSRF token from the request context or cookie.
func CSRFToken(r *http.Request) string {
	if tok, ok := r.Context().Value(csrfTokenKey).(string); ok && tok != "" {
		return tok
	}
	if c, err := r.Cookie(CSRFCookie); err == nil && c.Value != "" {
		return c.Value
	}
	return ""
}

// CSRFProtect mints a per-session CSRF cookie on safe methods and requires
// the X-CSRF-Token header or csrf_token form value on mutating methods.
// Mismatches get 403.
func CSRFProtect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			tok := ensureCSRFCookie(w, r)
			if tok != "" {
				r = r.WithContext(context.WithValue(r.Context(), csrfTokenKey, tok))
			}
			next.ServeHTTP(w, r)
			return
		}
		if !validCSRF(r) {
			http.Error(w, "invalid csrf token", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// CheckCSRF is an alias for CSRFProtect.
func CheckCSRF(next http.Handler) http.Handler {
	return CSRFProtect(next)
}

// ensureCSRFCookie sets the readable CSRF cookie when absent and returns the token.
func ensureCSRFCookie(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(CSRFCookie); err == nil && c.Value != "" {
		return c.Value
	}
	token, err := GenerateToken()
	if err != nil {
		return ""
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookie,
		Value:    token,
		Path:     "/",
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int((24 * time.Hour).Seconds()),
	})
	return token
}

// validCSRF compares the X-CSRF-Token header or csrf_token form field against the CSRF cookie.
func validCSRF(r *http.Request) bool {
	c, err := r.Cookie(CSRFCookie)
	if err != nil || c.Value == "" {
		return false
	}
	got := r.Header.Get(CSRFHeader)
	if got == "" {
		got = r.FormValue("csrf_token")
	}
	if got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(c.Value)) == 1
}
