package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Horac3/Primus/internal/credential"
	"github.com/Horac3/Primus/internal/model"
)

const (
	// loginTimeout bounds one login request so a hung login endpoint can't
	// stall the run forever.
	loginTimeout = 30 * time.Second
	// loginCanceled marks a login interrupted by Stop. Such logins are
	// counted as Cancelled in the summary, never as Failed.
	loginCanceled = "canceled"
	// maxLoginBody caps how much of a login response is read for extraction.
	maxLoginBody = 1 << 20
	// maxLoginFailureReasons is how many distinct failure reasons a run keeps.
	maxLoginFailureReasons = 5
)

// ValidatePool checks that pool can serve cfg before any login is sent:
// the login template only uses fields the pool has, the apply format
// uses {{token}}, and there are enough credentials for the concurrency
// unless reuse is allowed. Errors never contain credential values.
func ValidatePool(cfg model.TestConfig, pool model.CredentialPool) error {
	if len(pool.Credentials) == 0 {
		return fmt.Errorf("credential pool %q has no credentials", pool.Name)
	}
	if strings.TrimSpace(pool.Login.URL) == "" {
		return fmt.Errorf("credential pool %q has no login URL", pool.Name)
	}
	switch pool.Login.Extraction.From {
	case model.ExtractFromBody, model.ExtractFromHeader:
	default:
		return fmt.Errorf("credential pool %q: unknown extraction source %q (want body or header)",
			pool.Name, pool.Login.Extraction.From)
	}
	if strings.TrimSpace(pool.Login.Extraction.Path) == "" {
		return fmt.Errorf("credential pool %q has no extraction path", pool.Name)
	}

	fields := make(map[string]bool)
	for _, c := range pool.Credentials {
		for name := range c {
			fields[name] = true
		}
	}
	templates := []string{pool.Login.URL, pool.Login.Body}
	for _, v := range pool.Login.Headers {
		templates = append(templates, v)
	}
	for _, tmpl := range templates {
		for _, name := range credential.Placeholders(tmpl) {
			if !fields[name] {
				return fmt.Errorf("login template placeholder {{%s}} does not match any credential field", name)
			}
		}
	}

	_, format := applyDefaults(pool.Apply)
	names := credential.Placeholders(format)
	if len(names) != 1 || names[0] != "token" {
		return fmt.Errorf("apply format %q must contain {{token}} and no other placeholders", format)
	}

	if len(pool.Credentials) < cfg.Concurrency && !pool.AllowReuse {
		return fmt.Errorf("credential pool %q has %d credentials but concurrency is %d: add credentials, lower concurrency, or allow credential reuse",
			pool.Name, len(pool.Credentials), cfg.Concurrency)
	}
	return nil
}

func applyDefaults(a model.ApplySpec) (header, format string) {
	header, format = a.Header, a.Format
	if strings.TrimSpace(header) == "" {
		header = model.DefaultApplyHeader
	}
	if strings.TrimSpace(format) == "" {
		format = model.DefaultApplyFormat
	}
	return header, format
}

// Login sends one credential's login request and returns its outcome and
// the extracted token. It is the same path the run's login phase uses, so
// the UI can test a login spec against one credential.
func Login(ctx context.Context, pool model.CredentialPool, index int) (model.LoginOutcome, string) {
	client := newClient(1)
	defer client.CloseIdleConnections()
	return newPoolLogins(client, pool).login(ctx, index)
}

// poolLogins runs a pool's login phase and later re-logins, and reports
// them. outcomes and slots are written once by loginWorkers before any
// worker starts; the re-login counters are atomic because workers bump them.
type poolLogins struct {
	client *http.Client
	pool   model.CredentialPool

	outcomes   []model.LoginOutcome
	slots      []credentialSlot
	relogins   atomic.Int64
	reloginsOK atomic.Int64
}

// credentialSlot is the current token for one credential. Workers sharing
// a credential (reuse) coordinate re-login through it: only the first
// worker to see a 401 on the current generation logs in again; the others
// adopt the fresh token. This keeps single-session targets, which revoke
// older tokens on each login, from having workers revoke each other.
type credentialSlot struct {
	mu    sync.Mutex
	gen   int
	token string
}

func newPoolLogins(client *http.Client, pool model.CredentialPool) *poolLogins {
	return &poolLogins{client: client, pool: pool}
}

// loginWorkers logs in every credential the run's workers need, with at
// most LoginConcurrency logins in flight, and returns one workerAuth per
// worker whose credential logged in. Worker i uses credential
// i % len(pool); with reuse, a shared credential logs in once and its
// workers each hold a copy of the token.
func (p *poolLogins) loginWorkers(ctx context.Context, concurrency int) []workerAuth {
	distinct := min(concurrency, len(p.pool.Credentials))
	limit := p.pool.LoginConcurrency
	if limit <= 0 {
		limit = model.DefaultLoginConcurrency
	}

	outcomes := make([]model.LoginOutcome, distinct)
	tokens := make([]string, distinct)
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for i := 0; i < distinct; i++ {
		if ctx.Err() != nil {
			outcomes[i] = model.LoginOutcome{Index: i, ErrorClass: loginCanceled}
			continue
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			outcomes[i] = model.LoginOutcome{Index: i, ErrorClass: loginCanceled}
			continue
		}
		// select picks randomly when both cases are ready; never start a
		// login once the run is cancelled.
		if ctx.Err() != nil {
			<-sem
			outcomes[i] = model.LoginOutcome{Index: i, ErrorClass: loginCanceled}
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			outcomes[i], tokens[i] = p.login(ctx, i)
		}(i)
	}
	wg.Wait()
	p.outcomes = outcomes
	p.slots = make([]credentialSlot, distinct)
	for i, token := range tokens {
		p.slots[i].token = token
	}

	var workers []workerAuth
	for w := 0; w < concurrency; w++ {
		idx := w % distinct
		if !outcomes[idx].Success {
			continue
		}
		wa := workerAuth{auth: p.authFor(tokens[idx])}
		if p.pool.ReloginOn401 {
			wa.relogin = p.reloginFunc(idx)
		}
		workers = append(workers, wa)
	}
	return workers
}

// reloginFunc returns one worker's re-login for credential idx, counted
// separately from the login phase. held is the token generation the
// worker last used; it is touched only by that worker's goroutine. If
// another worker already re-logged in this credential, the fresh token is
// adopted without a new login. A re-login cut short by Stop or the run
// deadline is not counted.
func (p *poolLogins) reloginFunc(idx int) func(context.Context) (model.AuthConfig, bool) {
	held := 0
	return func(ctx context.Context) (model.AuthConfig, bool) {
		slot := &p.slots[idx]
		slot.mu.Lock()
		defer slot.mu.Unlock()
		if slot.gen != held {
			held = slot.gen
			return p.authFor(slot.token), true
		}
		outcome, token := p.login(ctx, idx)
		if !outcome.Success {
			if ctx.Err() == nil {
				p.relogins.Add(1)
			}
			return model.AuthConfig{}, false
		}
		p.relogins.Add(1)
		p.reloginsOK.Add(1)
		slot.gen++
		slot.token = token
		held = slot.gen
		return p.authFor(token), true
	}
}

// authFor turns a token into the AuthConfig applyAuth sends.
func (p *poolLogins) authFor(token string) model.AuthConfig {
	header, format := applyDefaults(p.pool.Apply)
	value, _ := credential.Render(format, map[string]string{"token": token}) // format validated by ValidatePool
	return model.AuthConfig{Type: model.AuthTypeCredentialPool, Header: header, Value: value}
}

// login sends credential idx's login request. The outcome's ErrorClass is
// a short, value-free reason suitable for the run report.
func (p *poolLogins) login(ctx context.Context, idx int) (model.LoginOutcome, string) {
	if idx < 0 || idx >= len(p.pool.Credentials) {
		return model.LoginOutcome{Index: idx, ErrorClass: "no such credential"}, ""
	}
	spec := p.pool.Login
	out, resp := sendSpec(ctx, p.client, spec, p.pool.Credentials[idx])
	out.Index = idx
	if !out.Success {
		return out, ""
	}
	token, err := credential.Extract(resp, spec.Extraction)
	if err != nil {
		out.Success = false
		out.ErrorClass = "token not found: " + err.Error()
		return out, ""
	}
	return out, token
}

// sendSpec renders spec from cred, sends it, and reports the outcome. On
// success (a 2xx response) it also returns the response for extraction.
// ErrorClass is short and value-free; it never embeds the rendered URL.
func sendSpec(ctx context.Context, client *http.Client, spec model.LoginSpec, cred model.Credential) (model.LoginOutcome, credential.Response) {
	var out model.LoginOutcome
	url, err := credential.Render(spec.URL, cred)
	if err != nil {
		out.ErrorClass = err.Error()
		return out, credential.Response{}
	}
	headers := make(http.Header, len(spec.Headers))
	for name, tmpl := range spec.Headers {
		value, err := credential.Render(tmpl, cred)
		if err != nil {
			out.ErrorClass = err.Error()
			return out, credential.Response{}
		}
		headers.Set(name, value)
	}
	var body io.Reader
	if spec.Body != "" {
		rendered, err := credential.RenderBody(spec.Body, cred, headers.Get("Content-Type"))
		if err != nil {
			out.ErrorClass = err.Error()
			return out, credential.Response{}
		}
		body = strings.NewReader(rendered)
	}
	method := strings.ToUpper(strings.TrimSpace(spec.Method))
	if method == "" {
		method = http.MethodPost
	}

	ctx, cancel := context.WithTimeout(ctx, loginTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		out.ErrorClass = "invalid login request"
		return out, credential.Response{}
	}
	req.Header = headers

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		out.Latency = time.Since(start)
		out.ErrorClass = classifyLoginError(ctx, err)
		return out, credential.Response{}
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxLoginBody))
	out.Latency = time.Since(start)
	out.StatusCode = resp.StatusCode
	if err != nil {
		out.ErrorClass = classifyLoginError(ctx, err)
		return out, credential.Response{}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		out.ErrorClass = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return out, credential.Response{}
	}
	out.Success = true
	return out, credential.Response{Header: resp.Header, Body: respBody}
}

// classifyLoginError reduces a transport error to a short reason. Raw
// errors are not reported: they can embed the rendered login URL.
func classifyLoginError(ctx context.Context, err error) string {
	// Judged by the context alone: ctx derives from the run, so it reports
	// Canceled only when the run was stopped (loginTimeout reports
	// DeadlineExceeded, a real timeout).
	if errors.Is(ctx.Err(), context.Canceled) {
		return loginCanceled
	}
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return "timeout"
	}
	return "connection error"
}

// summary reports the login phase plus any re-logins. Call it only after
// every worker has exited.
func (p *poolLogins) summary() *model.LoginSummary {
	s := summarizeOutcomes(p.outcomes)
	s.PoolID = p.pool.ID
	s.PoolName = p.pool.Name
	s.Relogins = int(p.relogins.Load())
	s.ReloginsSucceeded = int(p.reloginsOK.Load())
	return s
}

// summarizeOutcomes counts outcomes into a LoginSummary: succeeded,
// cancelled (never failed), failed with value-free reasons, and latency
// percentiles. Pool identity and re-login counters are left to the caller.
func summarizeOutcomes(outcomes []model.LoginOutcome) *model.LoginSummary {
	s := &model.LoginSummary{
		Attempted: len(outcomes),
		Outcomes:  append([]model.LoginOutcome(nil), outcomes...),
	}
	reasons := make(map[string]int)
	var latencies []int
	for _, o := range outcomes {
		switch {
		case o.Success:
			s.Succeeded++
		case o.ErrorClass == loginCanceled:
			s.Cancelled++
		default:
			s.Failed++
			reasons[o.ErrorClass]++
		}
		if o.Latency > 0 {
			latencies = append(latencies, int(o.Latency.Milliseconds()))
		}
	}
	s.P50Millis, s.P95Millis, _ = Percentiles(latencies)
	for cause, count := range reasons {
		s.FailureReasons = append(s.FailureReasons, model.ErrorDetail{Cause: cause, Count: count})
	}
	sort.Slice(s.FailureReasons, func(i, j int) bool {
		a, b := s.FailureReasons[i], s.FailureReasons[j]
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.Cause < b.Cause
	})
	if len(s.FailureReasons) > maxLoginFailureReasons {
		s.FailureReasons = s.FailureReasons[:maxLoginFailureReasons]
	}
	return s
}

// loginAbortedRun is the run reported when dispatch never started: every
// login failed (OutcomeFailNoLogins), or the run was stopped during the
// login phase. It never passes.
func loginAbortedRun(cfg model.TestConfig, login *model.LoginSummary, activeWorkers int) model.Run {
	now := time.Now()
	outcome := model.OutcomeFailNoLogins
	if activeWorkers > 0 {
		outcome = model.OutcomeFailNoRequests
	}
	return model.Run{
		ID:           fmt.Sprintf("run-%d", now.UnixNano()),
		TestConfigID: cfg.ID,
		Mode:         cfg.Mode,
		Concurrency:  cfg.Concurrency,
		StartedAt:    now,
		FinishedAt:   now,
		Outcome:      outcome,
		Passed:       false,
		Login:        login,
	}
}
