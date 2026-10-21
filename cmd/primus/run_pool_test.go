package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Horac3/Primus/internal/model"
	"github.com/Horac3/Primus/internal/store"
)

func TestRunCmd_CredentialPool(t *testing.T) {
	cases := []struct {
		name     string
		password string
		wantCode int
		wantHits bool
	}{
		{"logins succeed", "right", 0, true},
		{"no successful logins", "wrong", 1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			var protectedHits atomic.Int32
			mux := http.NewServeMux()
			mux.HandleFunc("POST /login", func(w http.ResponseWriter, r *http.Request) {
				if r.FormValue("password") != "right" {
					http.Error(w, "no", http.StatusUnauthorized)
					return
				}
				w.Header().Set("X-Auth-Token", "tok-"+r.FormValue("username"))
			})
			mux.HandleFunc("GET /items", func(w http.ResponseWriter, r *http.Request) {
				protectedHits.Add(1)
				if !strings.HasPrefix(r.Header.Get("X-Auth-Token"), "tok-") {
					w.WriteHeader(http.StatusUnauthorized)
				}
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()

			db := newTestDB(t)
			if err := store.SaveRequestDefinition(db, model.RequestDefinition{ID: "req", Method: "GET", URLTemplate: srv.URL + "/items"}); err != nil {
				t.Fatal(err)
			}
			pool := model.CredentialPool{
				ID: "pool", Name: "ci users",
				Credentials: []model.Credential{
					{"username": "a", "password": tc.password}, {"username": "b", "password": tc.password},
				},
				Login: model.LoginSpec{
					Method: "POST", URL: srv.URL + "/login",
					Headers:    map[string]string{"Content-Type": "application/x-www-form-urlencoded"},
					Body:       "username={{username}}&password={{password}}",
					Extraction: model.Extraction{From: model.ExtractFromHeader, Path: "X-Auth-Token"},
				},
				Apply: model.ApplySpec{Header: "X-Auth-Token", Format: "{{token}}"},
			}
			if err := store.SaveCredentialPool(db, pool); err != nil {
				t.Fatal(err)
			}
			cfgPath := writeConfigFile(t, model.TestConfig{
				ID: "cfg", RequestID: "req", Mode: model.ModeLoad, Concurrency: 2, TotalRequests: 6,
				Assertions:       model.Assertions{ExpectedStatus: []int{200}},
				CredentialPoolID: "pool",
			})
			var out bytes.Buffer

			// Act
			code := runCmd(context.Background(), []string{cfgPath}, &out, db)

			// Assert
			if code != tc.wantCode || (protectedHits.Load() > 0) != tc.wantHits {
				t.Errorf("exit %d with %d protected hits, want exit %d and hits=%v; output:\n%s",
					code, protectedHits.Load(), tc.wantCode, tc.wantHits, out.String())
			}
			if strings.Contains(out.String(), tc.password+"\"") || strings.Contains(out.String(), "tok-a") {
				t.Errorf("CLI output leaks a secret:\n%s", out.String())
			}
		})
	}
}

func TestRunCmd_AuthSourceNoneSendsNoAuth(t *testing.T) {
	// Arrange
	var authed atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			authed.Add(1)
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	db := newTestDB(t)
	if err := store.SaveRequestDefinition(db, model.RequestDefinition{ID: "req", Method: "GET", URLTemplate: srv.URL}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveGlobalAuth(db, model.AuthConfig{Type: model.AuthTypeBearer, Token: "global-token"}); err != nil {
		t.Fatal(err)
	}
	cfgPath := writeConfigFile(t, model.TestConfig{
		ID: "cfg", RequestID: "req", Mode: model.ModeAuthProbe, Concurrency: 1, TotalRequests: 3,
		Assertions: model.Assertions{ExpectedStatus: []int{401}},
		AuthSource: model.AuthSourceNone,
	})
	var out bytes.Buffer

	// Act
	code := runCmd(context.Background(), []string{cfgPath}, &out, db)

	// Assert
	if code != 0 || authed.Load() != 0 {
		t.Errorf("exit %d with %d authed attempts, want exit 0 and none; output:\n%s", code, authed.Load(), out.String())
	}
}

func TestRunCmd_AuthSourcePoolWithoutPool_ExitsTwo(t *testing.T) {
	db := newTestDB(t)
	if err := store.SaveRequestDefinition(db, model.RequestDefinition{ID: "req", Method: "GET", URLTemplate: "http://127.0.0.1:1"}); err != nil {
		t.Fatal(err)
	}
	cfgPath := writeConfigFile(t, model.TestConfig{
		ID: "cfg", RequestID: "req", Mode: model.ModeAuthProbe, Concurrency: 1, TotalRequests: 1,
		AuthSource: model.AuthSourcePool,
	})
	var out bytes.Buffer

	if code := runCmd(context.Background(), []string{cfgPath}, &out, db); code != 2 {
		t.Errorf("exit %d, want 2 for auth_source pool without credential_pool_id; output:\n%s", code, out.String())
	}
}
