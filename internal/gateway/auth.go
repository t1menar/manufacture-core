package gateway

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// SessionCookie is the name of the browser session cookie.
const SessionCookie = "mf_session"

// SessionTTL is how long a session cookie is valid.
const SessionTTL = 12 * time.Hour

// Auth checks the single admin token. Only its SHA-256 is kept in memory; session
// cookies are "expiry.HMAC(hash, expiry)", so any gateway replica can verify them
// and changing the token invalidates all sessions.
type Auth struct {
	hash [32]byte
	now  func() time.Time
}

// NewAuth returns an authenticator for the token with the given SHA-256.
func NewAuth(tokenHash [32]byte) *Auth { return &Auth{hash: tokenHash, now: time.Now} }

// TokenHash hashes a token the way the store does.
func TokenHash(token string) [32]byte { return sha256.Sum256([]byte(token)) }

func (a *Auth) tokenOK(token string) bool {
	h := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(h[:], a.hash[:]) == 1
}

func (a *Auth) sign(exp int64) string {
	m := hmac.New(sha256.New, a.hash[:])
	m.Write([]byte("mf_session:" + strconv.FormatInt(exp, 10)))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (a *Auth) newSession() (string, time.Time) {
	exp := a.now().Add(SessionTTL)
	return strconv.FormatInt(exp.Unix(), 10) + "." + a.sign(exp.Unix()), exp
}

func (a *Auth) sessionOK(v string) bool {
	ts, sig, ok := strings.Cut(v, ".")
	if !ok {
		return false
	}
	exp, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || a.now().Unix() >= exp {
		return false
	}
	return hmac.Equal([]byte(sig), []byte(a.sign(exp)))
}

// authenticate reports whether the request carries the token or a valid session.
// Unsafe requests authenticated by cookie must come from the same origin (CSRF).
func (a *Auth) authenticate(r *http.Request) (ok bool, reason string) {
	if h := r.Header.Get("Authorization"); h != "" {
		scheme, tok, _ := strings.Cut(h, " ")
		if !strings.EqualFold(scheme, "Bearer") || !a.tokenOK(strings.TrimSpace(tok)) {
			return false, "invalid admin token"
		}
		return true, ""
	}
	c, err := r.Cookie(SessionCookie)
	if err != nil {
		return false, "missing admin token (Authorization: Bearer <MF_ADMIN_TOKEN>) or session cookie"
	}
	if !a.sessionOK(c.Value) {
		return false, "session expired or invalid; sign in again"
	}
	if !safeMethod(r.Method) && !sameOrigin(r) {
		return false, "cross-origin request with a session cookie"
	}
	return true, ""
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		// Browsers send Origin on cross-site unsafe requests; its absence means same-site or a non-browser.
		return r.Header.Get("Sec-Fetch-Site") != "cross-site"
	}
	u, err := url.Parse(o)
	return err == nil && strings.EqualFold(u.Host, r.Host)
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ok, reason := s.auth.authenticate(r); !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="manufacture"`)
			problem(w, http.StatusUnauthorized, CodeUnauthorized, reason)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		problem(w, http.StatusBadRequest, CodeInvalidRequest, "body must be {\"token\": \"...\"}")
		return
	}
	if !s.auth.tokenOK(body.Token) {
		problem(w, http.StatusUnauthorized, CodeUnauthorized, "invalid admin token")
		return
	}
	v, exp := s.auth.newSession()
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: v, Path: "/", Expires: exp, //nolint:gosec // Secure is set when served over TLS (MF_SECURE_COOKIES); plain HTTP on loopback is the default.
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: s.secureCookies})
	_ = s.store.Audit(r.Context(), actorAdmin, "session.create", "session", map[string]any{"remote": r.RemoteAddr})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteSession(w http.ResponseWriter, _ *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: "", Path: "/", MaxAge: -1, //nolint:gosec // see createSession.
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: s.secureCookies})
	w.WriteHeader(http.StatusNoContent)
}
