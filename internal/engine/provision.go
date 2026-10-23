package engine

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"sync"

	"github.com/Horac3/Primus/internal/credential"
	"github.com/Horac3/Primus/internal/model"
)

const (
	secretLength = 24
	// secretLower/Upper/Digit/Symbol are the classes every generated secret
	// draws from, so it passes typical password rules and stays safe inside
	// JSON and form bodies.
	secretLower  = "abcdefghijklmnopqrstuvwxyz"
	secretUpper  = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	secretDigit  = "0123456789"
	secretSymbol = "-_"
	secretAll    = secretLower + secretUpper + secretDigit + secretSymbol
)

// GenerateSecret returns a random 24-character secret from crypto/rand with
// at least one lowercase letter, uppercase letter, digit and one of "-_".
func GenerateSecret() string {
	pick := func(set string) byte {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(set))))
		if err != nil {
			// crypto/rand does not fail on supported platforms; a broken entropy
			// source must never yield a predictable password.
			panic("crypto/rand unavailable: " + err.Error())
		}
		return set[n.Int64()]
	}
	b := make([]byte, secretLength)
	for i := range b {
		b[i] = pick(secretAll)
	}
	// Place one char of each class up front, then shuffle them into place.
	for i, set := range []string{secretLower, secretUpper, secretDigit, secretSymbol} {
		b[i] = pick(set)
	}
	for i := len(b) - 1; i > 0; i-- {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			// crypto/rand does not fail on supported platforms; a broken entropy
			// source must never yield a predictable password.
			panic("crypto/rand unavailable: " + err.Error())
		}
		j := n.Int64()
		b[i], b[j] = b[j], b[i]
	}
	return string(b)
}

// Provision signs up spec.Count accounts on the target (D-016) and returns a
// row for each sign-up that answered 2xx, in sequence order, plus a summary.
// Account k (0-based) renders {{seq}} as startSeq+k and gets its own
// {{secret}}. Failures add no row and are reported by class only; sign-ups
// not started or interrupted by cancellation count as Cancelled. The
// returned rows hold secrets: the caller must store them masked and never
// log them.
func Provision(ctx context.Context, spec model.ProvisionSpec, startSeq int) ([]model.Credential, *model.LoginSummary) {
	client := newClient(1)
	defer client.CloseIdleConnections()

	limit := spec.Concurrency
	if limit <= 0 {
		limit = model.DefaultLoginConcurrency
	}
	outcomes := make([]model.LoginOutcome, spec.Count)
	rows := make([]model.Credential, spec.Count)
	missing := 0
	var missMu sync.Mutex

	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for i := 0; i < spec.Count; i++ {
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
		// sign-up once cancelled.
		if ctx.Err() != nil {
			<-sem
			outcomes[i] = model.LoginOutcome{Index: i, ErrorClass: loginCanceled}
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			out, row, miss := provisionOne(ctx, client, spec, startSeq+i)
			out.Index = i
			outcomes[i], rows[i] = out, row
			if miss {
				missMu.Lock()
				missing++
				missMu.Unlock()
			}
		}(i)
	}
	wg.Wait()

	sum := summarizeOutcomes(outcomes)
	if missing > 0 {
		sum.FailureReasons = append(sum.FailureReasons, model.ErrorDetail{
			Cause: fmt.Sprintf("%s not found in response (account still created)", spec.ExtractField),
			Count: missing,
		})
	}
	var out []model.Credential
	for i, o := range outcomes {
		if o.Success {
			out = append(out, rows[i])
		}
	}
	return out, sum
}

// provisionOne renders account seq's row, signs it up, and, on success,
// saves the extracted field into the row. miss reports a 2xx whose
// extraction found nothing: the account exists, so the row is kept.
func provisionOne(ctx context.Context, client *http.Client, spec model.ProvisionSpec, seq int) (out model.LoginOutcome, row model.Credential, miss bool) {
	vars := map[string]string{"seq": strconv.Itoa(seq), "secret": GenerateSecret()}
	row = make(model.Credential, len(spec.Fields)+1)
	for name, tmpl := range spec.Fields {
		v, err := credential.Render(tmpl, vars)
		if err != nil {
			return model.LoginOutcome{ErrorClass: err.Error()}, nil, false
		}
		row[name] = v
	}
	out, resp := sendSpec(ctx, client, spec.Signup, row)
	if !out.Success {
		return out, nil, false
	}
	if spec.ExtractField != "" {
		v, err := credential.Extract(resp, spec.Signup.Extraction)
		if err != nil {
			miss = true
			v = ""
		}
		row[spec.ExtractField] = v
	}
	return out, row, miss
}
