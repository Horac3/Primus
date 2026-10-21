package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Horac3/Primus/internal/model"
	"github.com/Horac3/Primus/internal/store"
)

// A rate run that drops requests exits 1 with the rate fields in the JSON
// summary, so CI fails when the target rate was not held (FR-RATE-02/03).
func TestRunCmd_RateNotSustained_ExitsOneWithRateFields(t *testing.T) {
	// Arrange: one slot, 300ms responses, 50 due per second.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(300 * time.Millisecond):
		}
	}))
	defer srv.Close()
	db := newTestDB(t)
	reqDef := model.RequestDefinition{ID: "req-cli-rate", Name: "slow", Method: "GET", URLTemplate: srv.URL}
	if err := store.SaveRequestDefinition(db, reqDef); err != nil {
		t.Fatalf("SaveRequestDefinition: %v", err)
	}
	cfgPath := writeConfigFile(t, model.TestConfig{
		ID: "cfg-cli-rate", RequestID: "req-cli-rate", Mode: model.ModeRate,
		Concurrency: 1, Duration: time.Second, TargetRate: 50,
	})
	var out bytes.Buffer

	// Act
	code := runCmd(context.Background(), []string{cfgPath}, &out, db)

	// Assert
	if code != 1 {
		t.Errorf("exit code = %d, want 1; output: %s", code, out.String())
	}
	var got struct {
		Outcome model.RunOutcome `json:"outcome"`
		Summary struct {
			Dropped    int     `json:"dropped"`
			TargetRate float64 `json:"target_rate"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, out.String())
	}
	if got.Outcome != model.OutcomeFailRateNotSustained || got.Summary.Dropped == 0 || got.Summary.TargetRate != 50 {
		t.Errorf("outcome %s, dropped %d, target %g; want fail_rate_not_sustained, >0, 50",
			got.Outcome, got.Summary.Dropped, got.Summary.TargetRate)
	}
}
