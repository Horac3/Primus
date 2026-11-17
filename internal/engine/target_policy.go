package engine

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/Horac3/Primus/internal/model"
)

// ErrProtectedHost is returned when a run would send heavy traffic to a protected host (D-021).
var ErrProtectedHost = errors.New("protected host")

// TargetPolicy keeps heavy runs away from hosts the operator marked as protected, usually production.
// Light runs (a few sequential requests, a smoke check) may still reach them.
type TargetPolicy struct {
	// Hosts are exact hostnames or "*.example.com" patterns (the pattern also covers example.com).
	Hosts []string
	// A light run is a count-based load run within both limits.
	LightMaxConcurrency int
	LightMaxRequests    int
}

const (
	defaultLightMaxConcurrency = 2
	defaultLightMaxRequests    = 50
)

// LoadTargetPolicy reads PRIMUS_PROTECTED_HOSTS (comma-separated), PRIMUS_PROTECTED_MAX_CONCURRENCY
// and PRIMUS_PROTECTED_MAX_REQUESTS. With no hosts listed the policy allows everything.
func LoadTargetPolicy(getenv func(string) string) (TargetPolicy, error) {
	p := TargetPolicy{LightMaxConcurrency: defaultLightMaxConcurrency, LightMaxRequests: defaultLightMaxRequests}
	for _, h := range strings.Split(getenv("PRIMUS_PROTECTED_HOSTS"), ",") {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			p.Hosts = append(p.Hosts, h)
		}
	}
	for _, f := range []struct {
		name string
		dst  *int
	}{
		{"PRIMUS_PROTECTED_MAX_CONCURRENCY", &p.LightMaxConcurrency},
		{"PRIMUS_PROTECTED_MAX_REQUESTS", &p.LightMaxRequests},
	} {
		raw := strings.TrimSpace(getenv(f.name))
		if raw == "" {
			continue
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return TargetPolicy{}, fmt.Errorf("%s must be a whole number >= 1 (got %q)", f.name, raw)
		}
		*f.dst = n
	}
	return p, nil
}

// Protects reports whether host is on the protected list.
func (p TargetPolicy) Protects(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, h := range p.Hosts {
		if suffix, ok := strings.CutPrefix(h, "*."); ok {
			if host == suffix || strings.HasSuffix(host, "."+suffix) {
				return true
			}
		} else if host == h {
			return true
		}
	}
	return false
}

// light reports whether cfg is small enough to run against a protected host without an override.
func (p TargetPolicy) light(cfg model.TestConfig) bool {
	return cfg.Mode == model.ModeLoad && cfg.Duration == 0 && cfg.BurstPattern == nil &&
		cfg.TotalRequests > 0 && cfg.TotalRequests <= p.LightMaxRequests &&
		cfg.Concurrency <= p.LightMaxConcurrency
}

// Check refuses a heavy run against a protected host unless cfg.AllowProtectedHost names that exact host.
// targetURL is the resolved URL the run sends. Errors wrap ErrProtectedHost.
func (p TargetPolicy) Check(cfg model.TestConfig, targetURL string) error {
	if len(p.Hosts) == 0 {
		return nil
	}
	u, err := url.Parse(targetURL)
	if err != nil || u.Hostname() == "" {
		return fmt.Errorf("%w: cannot read a host from the endpoint URL, and protected hosts are configured", ErrProtectedHost)
	}
	host := strings.ToLower(u.Hostname())
	if !p.Protects(host) || p.light(cfg) {
		return nil
	}
	if strings.EqualFold(strings.TrimSpace(cfg.AllowProtectedHost), host) {
		return nil
	}
	return fmt.Errorf("%w: %s is listed in PRIMUS_PROTECTED_HOSTS; only count-based load runs with at most %d workers "+
		"and %d requests may reach it. Point the endpoint at a non-production host, or, if you own %s and mean "+
		"to load it, set allow_protected_host to %q", ErrProtectedHost, host, p.LightMaxConcurrency, p.LightMaxRequests, host, host)
}
