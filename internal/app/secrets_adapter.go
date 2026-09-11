package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/alternayte/sluice/internal/execution"
	"github.com/alternayte/sluice/internal/secret"
)

// ReasonSecretProviderError fails a task when the provider of a defined secret fails.
const ReasonSecretProviderError = "secret_provider_error"

// secretResolver adapts the secret service to execution.SecretResolver. A key that no
// scope defines, or that its provider does not have, fails the task with
// secret_not_found and the scopes searched (REQ-SEC-009).
type secretResolver struct{ s *secret.Service }

func (r secretResolver) Resolve(ctx context.Context, namespace, key string) (string, error) {
	v, err := r.s.Resolve(ctx, namespace, key)
	var nf *secret.NotFoundError
	if errors.As(err, &nf) {
		return "", &execution.SecretNotFoundError{Key: nf.Key, Scopes: nf.Scopes}
	}
	var re *secret.ResolveError
	if errors.As(err, &re) {
		reason := ReasonSecretProviderError
		if errors.Is(err, secret.ErrValueNotFound) {
			reason = execution.ReasonSecretNotFound
		}
		return "", &execution.PlanError{Reason: reason, Msg: re.Error()}
	}
	return v, err
}

// masterKeyring builds the keyring from SLUICE_MASTER_KEYS. An empty value gives a
// keyring without keys: builtin writes then return 409 (REQ-SEC-010).
func masterKeyring(cfg *Config) (*secret.Keyring, error) {
	mks, err := ParseMasterKeys(cfg.MasterKeys)
	if err != nil {
		return nil, fmt.Errorf("SLUICE_MASTER_KEYS: %w", err)
	}
	keys := make([]secret.Key, 0, len(mks))
	for _, k := range mks {
		keys = append(keys, secret.Key{ID: k.ID, Key: k.Key})
	}
	return secret.NewKeyring(keys)
}
