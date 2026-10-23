package engine

import (
	"fmt"

	"github.com/Horac3/Primus/internal/model"
)

// MinMaxRate is the smallest accepted max_rate (one attempt per ~17
// minutes); smaller values would overflow the pacing interval.
const MinMaxRate = 0.001

// ResolveAuth turns cfg.AuthSource into the auth and pool RunWithPool
// takes. global is the stored global auth; pool is the run's candidate
// pool (the active one in the UI, credential_pool_id in the CLI), or nil.
// Dispatch still goes through applyAuth only.
func ResolveAuth(cfg model.TestConfig, global model.AuthConfig, pool *model.CredentialPool) (model.AuthConfig, *model.CredentialPool, error) {
	switch cfg.AuthSource {
	case model.AuthSourceDefault:
		return global, pool, nil
	case model.AuthSourceGlobal:
		return global, nil, nil
	case model.AuthSourcePool:
		if pool == nil {
			return model.AuthConfig{}, nil, fmt.Errorf("auth_source pool needs a credential pool, and none is active")
		}
		return global, pool, nil
	case model.AuthSourceNone:
		return model.AuthConfig{}, nil, nil
	default:
		return model.AuthConfig{}, nil, fmt.Errorf("unknown auth_source %q (want global, pool or none)", cfg.AuthSource)
	}
}
