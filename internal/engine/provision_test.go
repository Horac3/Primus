package engine_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Horac3/Primus/internal/engine"
	"github.com/Horac3/Primus/internal/model"
)

// signupTarget is a throwaway sign-up endpoint. POST /signup records each
// {email,password} pair and answers 201 with {"id":"u-<n>"}; emails listed in
// reject answer 409.
type signupTarget struct {
	srv    *httptest.Server
	delay  time.Duration
	reject map[string]bool

	hits     atomic.Int32
	inFlight atomic.Int32
	maxIn    atomic.Int32

	mu    sync.Mutex
	seen  map[string]string // email -> password
	block chan struct{}
}

func newSignupTarget(t *testing.T) *signupTarget {
	t.Helper()
	s := &signupTarget{seen: map[string]string{}, reject: map[string]bool{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /signup", s.signup)
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

func (s *signupTarget) signup(w http.ResponseWriter, r *http.Request) {
	n := s.hits.Add(1)
	cur := s.inFlight.Add(1)
	defer s.inFlight.Add(-1)
	for {
		m := s.maxIn.Load()
		if cur <= m || s.maxIn.CompareAndSwap(m, cur) {
			break
		}
	}
	var in struct{ Email, Password string }
	_ = json.NewDecoder(r.Body).Decode(&in)
	if s.block != nil && n > 2 {
		<-r.Context().Done()
		return
	}
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	if s.reject[in.Email] {
		http.Error(w, `{"error":"exists"}`, http.StatusConflict)
		return
	}
	s.mu.Lock()
	s.seen[in.Email] = in.Password
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	fmt.Fprintf(w, `{"id":"u-%d"}`, n)
}

func (s *signupTarget) spec(count int) model.ProvisionSpec {
	return model.ProvisionSpec{
		Count: count,
		Fields: map[string]string{
			"email":    "u{{seq}}@x.test",
			"password": "{{secret}}",
		},
		Signup: model.LoginSpec{
			URL:     s.srv.URL + "/signup",
			Headers: map[string]string{"Content-Type": "application/json"},
			Body:    `{"email":"{{email}}","password":"{{password}}"}`,
		},
	}
}

func TestProvision_DistinctAccountsOneRowPerSuccess(t *testing.T) {
	s := newSignupTarget(t)
	rows, sum := engine.Provision(context.Background(), s.spec(5), 1)

	if len(rows) != 5 || sum.Attempted != 5 || sum.Succeeded != 5 || sum.Failed != 0 {
		t.Fatalf("rows=%d summary=%+v", len(rows), sum)
	}
	emails, secrets := map[string]bool{}, map[string]bool{}
	for _, r := range rows {
		emails[r["email"]] = true
		secrets[r["password"]] = true
		if s.seen[r["email"]] != r["password"] {
			t.Errorf("row %q does not match what the target received", r["email"])
		}
	}
	if len(emails) != 5 || len(secrets) != 5 {
		t.Fatalf("emails=%d secrets=%d, want 5 distinct each", len(emails), len(secrets))
	}
	if !emails["u1@x.test"] || !emails["u5@x.test"] {
		t.Errorf("seq should run 1..5, got %v", emails)
	}
}

func TestProvision_StartSeqOffsetsSeq(t *testing.T) {
	s := newSignupTarget(t)
	rows, _ := engine.Provision(context.Background(), s.spec(2), 11)
	if rows[0]["email"] != "u11@x.test" || rows[1]["email"] != "u12@x.test" {
		t.Fatalf("got %q, %q", rows[0]["email"], rows[1]["email"])
	}
}

func TestProvision_FailuresAddNoRowAndAreCounted(t *testing.T) {
	s := newSignupTarget(t)
	s.reject["u2@x.test"] = true
	s.reject["u4@x.test"] = true
	rows, sum := engine.Provision(context.Background(), s.spec(5), 1)

	if len(rows) != 3 || sum.Succeeded != 3 || sum.Failed != 2 {
		t.Fatalf("rows=%d summary=%+v", len(rows), sum)
	}
	if len(sum.FailureReasons) != 1 || sum.FailureReasons[0].Cause != "HTTP 409" || sum.FailureReasons[0].Count != 2 {
		t.Fatalf("reasons=%+v", sum.FailureReasons)
	}
	for _, r := range rows {
		if r["email"] == "u2@x.test" || r["email"] == "u4@x.test" {
			t.Errorf("rejected account %q was stored", r["email"])
		}
	}
}

func TestProvision_TransportErrorIsFailureWithoutRawCause(t *testing.T) {
	s := newSignupTarget(t)
	spec := s.spec(2)
	s.srv.Close()
	rows, sum := engine.Provision(context.Background(), spec, 1)
	if len(rows) != 0 || sum.Failed != 2 {
		t.Fatalf("rows=%d summary=%+v", len(rows), sum)
	}
	if got := sum.FailureReasons[0].Cause; got != "connection error" {
		t.Errorf("cause = %q, want connection error (raw errors embed the URL)", got)
	}
}

func TestProvision_ExtractionSavesFieldAndMissIsReported(t *testing.T) {
	s := newSignupTarget(t)
	spec := s.spec(3)
	spec.ExtractField = "user_id"
	spec.Signup.Extraction = model.Extraction{From: model.ExtractFromBody, Path: "id"}
	rows, sum := engine.Provision(context.Background(), spec, 1)
	if len(rows) != 3 {
		t.Fatalf("rows=%d", len(rows))
	}
	for _, r := range rows {
		if !strings.HasPrefix(r["user_id"], "u-") {
			t.Errorf("user_id = %q", r["user_id"])
		}
	}

	spec.Signup.Extraction.Path = "no.such.path"
	rows, sum = engine.Provision(context.Background(), spec, 10)
	if len(rows) != 3 || sum.Succeeded != 3 {
		t.Fatalf("a missing extraction must keep the account: rows=%d summary=%+v", len(rows), sum)
	}
	found := false
	for _, fr := range sum.FailureReasons {
		found = found || strings.Contains(fr.Cause, "user_id")
	}
	if !found {
		t.Errorf("miss not reported: %+v", sum.FailureReasons)
	}
}

func TestProvision_ConcurrencyIsBounded(t *testing.T) {
	s := newSignupTarget(t)
	s.delay = 30 * time.Millisecond
	spec := s.spec(12)
	spec.Concurrency = 3
	_, sum := engine.Provision(context.Background(), spec, 1)
	if sum.Succeeded != 12 {
		t.Fatalf("summary=%+v", sum)
	}
	if got := s.maxIn.Load(); got > 3 || got < 2 {
		t.Fatalf("max in-flight = %d, want 2..3", got)
	}
}

func TestProvision_CancelKeepsFinishedRowsAndCountsRestCancelled(t *testing.T) {
	s := newSignupTarget(t)
	s.block = make(chan struct{}) // logins after the 2nd hang until cancelled
	spec := s.spec(8)
	spec.Concurrency = 1

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for s.hits.Load() < 3 {
			time.Sleep(5 * time.Millisecond)
		}
		cancel()
	}()
	start := time.Now()
	rows, sum := engine.Provision(ctx, spec, 1)
	if time.Since(start) > 5*time.Second {
		t.Fatal("cancel was not prompt")
	}
	if len(rows) != 2 || sum.Succeeded != 2 {
		t.Fatalf("rows=%d summary=%+v", len(rows), sum)
	}
	if sum.Failed != 0 || sum.Cancelled != 6 || sum.Attempted != 8 {
		t.Fatalf("cancelled requests must not count as failed: %+v", sum)
	}
}

func TestGenerateSecret(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		s := engine.GenerateSecret()
		if len(s) != 24 {
			t.Fatalf("len = %d", len(s))
		}
		var lower, upper, digit, sym bool
		for _, c := range s {
			switch {
			case c >= 'a' && c <= 'z':
				lower = true
			case c >= 'A' && c <= 'Z':
				upper = true
			case c >= '0' && c <= '9':
				digit = true
			case c == '-' || c == '_':
				sym = true
			default:
				t.Fatalf("unexpected character %q in %q", c, s)
			}
		}
		if !lower || !upper || !digit || !sym {
			t.Fatalf("%q misses a character class", s)
		}
		if seen[s] {
			t.Fatalf("duplicate secret %q", s)
		}
		seen[s] = true
	}
}
