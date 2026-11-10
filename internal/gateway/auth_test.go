package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSessionCookie(t *testing.T) {
	a := NewAuth(TokenHash("token-one-0123456789abcdef0123456789"))
	now := time.Unix(1_800_000_000, 0)
	a.now = func() time.Time { return now }
	v, _ := a.newSession()
	if !a.sessionOK(v) {
		t.Fatal("fresh session rejected")
	}
	other := NewAuth(TokenHash("token-two-0123456789abcdef0123456789"))
	other.now = a.now
	if other.sessionOK(v) {
		t.Fatal("session signed with another token accepted")
	}
	for _, bad := range []string{"", "x", "1.", "abc.def", v + "x"} {
		if a.sessionOK(bad) {
			t.Fatalf("accepted %q", bad)
		}
	}
	now = now.Add(SessionTTL + time.Second)
	if a.sessionOK(v) {
		t.Fatal("expired session accepted")
	}
}

func TestBearer(t *testing.T) {
	a := NewAuth(TokenHash("secret-0123456789abcdef0123456789ab"))
	r := httptest.NewRequest("GET", "/api/v1/system", http.NoBody)
	r.Header.Set("Authorization", "bearer secret-0123456789abcdef0123456789ab")
	if ok, _ := a.authenticate(r); !ok {
		t.Fatal("scheme must be case-insensitive")
	}
	r.Header.Set("Authorization", "Basic secret-0123456789abcdef0123456789ab")
	if ok, _ := a.authenticate(r); ok {
		t.Fatal("Basic accepted")
	}
}

func TestApplyInput(t *testing.T) {
	fs, err := applyInput("application/json", []byte(`{"files":[{"name":"a.yaml","content":"x: 1"},{"content":"y: 2"}]}`))
	if err != nil || len(fs) != 2 || fs[0].Name != "a.yaml" || fs[1].Name != "file 2" {
		t.Fatalf("files form: %+v %v", fs, err)
	}
	fs, err = applyInput("application/json", []byte(`{"apiVersion":"manufacture.dev/v1"}`))
	if err != nil || len(fs) != 1 || fs[0].Name != "request" {
		t.Fatalf("document form: %+v %v", fs, err)
	}
	if _, err := applyInput("image/png", nil); err == nil {
		t.Fatal("png accepted")
	}
}
