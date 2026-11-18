package engine_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Horac3/Primus/internal/engine"
	"github.com/Horac3/Primus/internal/model"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// FR-GUARD-01: the protected host list and light-run limits come from the environment.
func TestLoadTargetPolicy_ReadsHostsAndLimits(t *testing.T) {
	p, err := engine.LoadTargetPolicy(envOf(map[string]string{
		"PRIMUS_PROTECTED_HOSTS":           " API.Example.com , *.prod.example.net ,, ",
		"PRIMUS_PROTECTED_MAX_CONCURRENCY": "3",
		"PRIMUS_PROTECTED_MAX_REQUESTS":    "10",
	}))
	if err != nil {
		t.Fatalf("LoadTargetPolicy: %v", err)
	}
	if len(p.Hosts) != 2 || p.Hosts[0] != "api.example.com" || p.Hosts[1] != "*.prod.example.net" {
		t.Errorf("Hosts = %q, want lower-cased, trimmed, empties dropped", p.Hosts)
	}
	if p.LightMaxConcurrency != 3 || p.LightMaxRequests != 10 {
		t.Errorf("limits = %d/%d, want 3/10", p.LightMaxConcurrency, p.LightMaxRequests)
	}
}

func TestLoadTargetPolicy_DefaultsAndBadNumbers(t *testing.T) {
	p, err := engine.LoadTargetPolicy(envOf(nil))
	if err != nil {
		t.Fatalf("LoadTargetPolicy: %v", err)
	}
	if len(p.Hosts) != 0 || p.LightMaxConcurrency != 2 || p.LightMaxRequests != 50 {
		t.Errorf("defaults = %+v, want no hosts, 2 concurrency, 50 requests", p)
	}
	for _, bad := range []string{"0", "-1", "two"} {
		if _, err := engine.LoadTargetPolicy(envOf(map[string]string{"PRIMUS_PROTECTED_MAX_REQUESTS": bad})); err == nil {
			t.Errorf("PRIMUS_PROTECTED_MAX_REQUESTS=%q: want an error", bad)
		}
	}
}

func TestTargetPolicy_Protects(t *testing.T) {
	p := engine.TargetPolicy{Hosts: []string{"api.example.com", "*.prod.example.net"}}
	cases := []struct {
		host string
		want bool
	}{
		{"api.example.com", true},
		{"API.EXAMPLE.COM", true},
		{"staging.example.com", false},
		{"prod.example.net", true},
		{"eu.prod.example.net", true},
		{"notprod.example.net", false},
		{"localhost", false},
	}
	for _, c := range cases {
		if got := p.Protects(c.host); got != c.want {
			t.Errorf("Protects(%q) = %v, want %v", c.host, got, c.want)
		}
	}
}

func lightLoad() model.TestConfig {
	return model.TestConfig{Mode: model.ModeLoad, Concurrency: 1, TotalRequests: 5}
}

// FR-GUARD-02: light runs may reach a protected host; heavy runs are refused unless
// allow_protected_host names the host exactly.
func TestTargetPolicy_Check(t *testing.T) {
	p := engine.TargetPolicy{Hosts: []string{"api.example.com"}, LightMaxConcurrency: 2, LightMaxRequests: 50}
	heavy := []struct {
		name string
		cfg  model.TestConfig
	}{
		{"too many workers", model.TestConfig{Mode: model.ModeLoad, Concurrency: 3, TotalRequests: 5}},
		{"too many requests", model.TestConfig{Mode: model.ModeLoad, Concurrency: 1, TotalRequests: 51}},
		{"duration run", model.TestConfig{Mode: model.ModeLoad, Concurrency: 1, Duration: time.Second}},
		{"burst", model.TestConfig{Mode: model.ModeBurst, Concurrency: 1, TotalRequests: 5}},
		{"soak", model.TestConfig{Mode: model.ModeSoak, Concurrency: 1, Duration: time.Second}},
		{"rate", model.TestConfig{Mode: model.ModeRate, Concurrency: 1, TargetRate: 1, Duration: time.Second}},
		{"auth probe", model.TestConfig{Mode: model.ModeAuthProbe, Concurrency: 1, TotalRequests: 5}},
	}
	for _, h := range heavy {
		t.Run(h.name, func(t *testing.T) {
			err := p.Check(h.cfg, "https://api.example.com/v1/orders?x=1")
			if !errors.Is(err, engine.ErrProtectedHost) {
				t.Fatalf("Check = %v, want ErrProtectedHost", err)
			}
			h.cfg.AllowProtectedHost = "api.example.com"
			if err := p.Check(h.cfg, "https://api.example.com/v1/orders"); err != nil {
				t.Errorf("with allow_protected_host: %v, want nil", err)
			}
			h.cfg.AllowProtectedHost = "other.example.com"
			if err := p.Check(h.cfg, "https://api.example.com/v1/orders"); !errors.Is(err, engine.ErrProtectedHost) {
				t.Errorf("allow for another host: %v, want ErrProtectedHost", err)
			}
		})
	}
	if err := p.Check(lightLoad(), "https://api.example.com/health"); err != nil {
		t.Errorf("light run on protected host: %v, want nil", err)
	}
	big := model.TestConfig{Mode: model.ModeLoad, Concurrency: 100, Duration: time.Hour}
	if err := p.Check(big, "https://staging.example.com/"); err != nil {
		t.Errorf("heavy run on unprotected host: %v, want nil", err)
	}
	if err := (engine.TargetPolicy{}).Check(big, "https://api.example.com/"); err != nil {
		t.Errorf("empty policy: %v, want nil", err)
	}
}

// A policy with protected hosts refuses a URL it cannot read a host from, rather than guess.
func TestTargetPolicy_Check_UnparsableURLFailsClosed(t *testing.T) {
	p := engine.TargetPolicy{Hosts: []string{"api.example.com"}, LightMaxConcurrency: 2, LightMaxRequests: 50}
	big := model.TestConfig{Mode: model.ModeLoad, Concurrency: 10, TotalRequests: 1000}
	if err := p.Check(big, "not a url"); !errors.Is(err, engine.ErrProtectedHost) {
		t.Errorf("Check = %v, want ErrProtectedHost", err)
	}
}
