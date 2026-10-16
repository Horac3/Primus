package engine_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Horac3/Primus/internal/model"
)

// authFixture is a throwaway target with pre-existing users: POST /login
// returns {"data":{"token":...}} for a correct password, and GET /me
// accepts "Authorization: Bearer <token>" and records whose token it was.
type authFixture struct {
	srv      *httptest.Server
	password map[string]string

	// loginDelay slows every login; loginBlock, when set, holds logins
	// until the client gives up.
	loginDelay time.Duration
	loginBlock bool
	// firstTokenExpired makes /me reject each user's first-issued token,
	// so only a re-login gets through.
	firstTokenExpired bool
	// revokeOnLogin makes each new login invalidate that user's older
	// tokens, like a single-session target.
	revokeOnLogin bool
	// meDelay slows the protected endpoint so in-flight overlap is visible.
	meDelay time.Duration

	loginHits     atomic.Int32
	loginInFlight atomic.Int32
	loginMaxIn    atomic.Int32
	meHits        atomic.Int32

	mu          sync.Mutex
	loginsByUse map[string]int    // user -> number of logins issued
	tokenUser   map[string]string // token -> user
	tokenGen    map[string]int    // token -> per-user login generation
	seenUsers   map[string]int    // user -> authenticated /me hits
	authHeaders map[string]int    // raw Authorization header -> /me hits
	inFlight    map[string]int    // token -> concurrent /me requests now
	maxPerToken int               // highest concurrent /me requests on one token
}

func newAuthFixture(t *testing.T, users int) *authFixture {
	t.Helper()
	f := &authFixture{
		password:    make(map[string]string, users),
		loginsByUse: map[string]int{},
		tokenUser:   map[string]string{},
		tokenGen:    map[string]int{},
		seenUsers:   map[string]int{},
		authHeaders: map[string]int{},
		inFlight:    map[string]int{},
	}
	for i := 0; i < users; i++ {
		f.password[fmt.Sprintf("user%02d", i)] = fmt.Sprintf("pw-%02d", i)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /login", f.login)
	mux.HandleFunc("GET /me", f.me)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *authFixture) login(w http.ResponseWriter, r *http.Request) {
	f.loginHits.Add(1)
	n := f.loginInFlight.Add(1)
	defer f.loginInFlight.Add(-1)
	for {
		max := f.loginMaxIn.Load()
		if n <= max || f.loginMaxIn.CompareAndSwap(max, n) {
			break
		}
	}
	// Read the body first: net/http only notices a client disconnect (and
	// cancels r.Context) once the request body has been consumed.
	var in struct{ Username, Password string }
	decodeErr := json.NewDecoder(r.Body).Decode(&in)
	_, _ = io.Copy(io.Discard, r.Body)
	if f.loginBlock {
		<-r.Context().Done()
		return
	}
	if f.loginDelay > 0 {
		time.Sleep(f.loginDelay)
	}
	if decodeErr != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	want, ok := f.password[in.Username]
	if !ok || want != in.Password {
		http.Error(w, `{"error":"invalid credentials"}`, http.StatusUnauthorized)
		return
	}
	f.mu.Lock()
	f.loginsByUse[in.Username]++
	gen := f.loginsByUse[in.Username]
	token := fmt.Sprintf("tok-%s-%d", in.Username, gen)
	f.tokenUser[token] = in.Username
	f.tokenGen[token] = gen
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"data":{"token":%q}}`, token)
}

func (f *authFixture) me(w http.ResponseWriter, r *http.Request) {
	f.meHits.Add(1)
	header := r.Header.Get("Authorization")
	token := strings.TrimPrefix(header, "Bearer ")
	f.mu.Lock()
	f.authHeaders[header]++
	user, known := f.tokenUser[token]
	expired := (f.firstTokenExpired && f.tokenGen[token] == 1) ||
		(f.revokeOnLogin && f.tokenGen[token] != f.loginsByUse[user])
	if known && !expired {
		f.seenUsers[user]++
		f.inFlight[token]++
		if f.inFlight[token] > f.maxPerToken {
			f.maxPerToken = f.inFlight[token]
		}
	}
	f.mu.Unlock()
	if !known || expired {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if f.meDelay > 0 {
		time.Sleep(f.meDelay)
	}
	f.mu.Lock()
	f.inFlight[token]--
	f.mu.Unlock()
	fmt.Fprintf(w, `{"user":%q}`, user)
}

// pool builds a credential pool for the first n fixture users. Users whose
// index is in wrong get a bad password.
func (f *authFixture) pool(n int, wrong ...int) model.CredentialPool {
	bad := map[int]bool{}
	for _, i := range wrong {
		bad[i] = true
	}
	creds := make([]model.Credential, n)
	for i := range creds {
		user := fmt.Sprintf("user%02d", i)
		pw := f.password[user]
		if bad[i] {
			pw = "wrong-password"
		}
		creds[i] = model.Credential{"username": user, "password": pw}
	}
	return model.CredentialPool{
		ID:          "fixture-pool",
		Name:        "fixture users",
		Credentials: creds,
		Login: model.LoginSpec{
			Method:     http.MethodPost,
			URL:        f.srv.URL + "/login",
			Headers:    map[string]string{"Content-Type": "application/json"},
			Body:       `{"username":"{{username}}","password":"{{password}}"}`,
			Extraction: model.Extraction{From: model.ExtractFromBody, Path: "data.token"},
		},
	}
}

func (f *authFixture) meRequest() model.RequestDefinition {
	return model.RequestDefinition{ID: "me", Method: http.MethodGet, URLTemplate: f.srv.URL + "/me"}
}

func (f *authFixture) snapshotSeen() map[string]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]int, len(f.seenUsers))
	for k, v := range f.seenUsers {
		out[k] = v
	}
	return out
}

func (f *authFixture) loginsFor(user string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.loginsByUse[user]
}

func (f *authFixture) snapshotHeaders() map[string]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]int, len(f.authHeaders))
	for k, v := range f.authHeaders {
		out[k] = v
	}
	return out
}
