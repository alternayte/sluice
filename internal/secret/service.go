package secret

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alternayte/sluice/internal/audit"
	"github.com/alternayte/sluice/internal/flow"
	"github.com/alternayte/sluice/internal/kernel"
	"github.com/alternayte/sluice/internal/platform/clock"
	"github.com/alternayte/sluice/internal/platform/httpx"
	"github.com/alternayte/sluice/internal/secret/secretdb"
)

// DefaultCacheTTL is the default cache lifetime of external values (REQ-SEC-005).
const DefaultCacheTTL = 60 * time.Second

// Errors of the secret feature.
var (
	ErrBuiltinDisabled   = httpx.Errorf(http.StatusConflict, "builtin_provider_disabled", "builtin secrets need SLUICE_MASTER_KEYS")
	ErrSecretNotFound    = httpx.Errorf(http.StatusNotFound, "secret_not_found", "secret not found")
	ErrProviderNotFound  = httpx.Errorf(http.StatusNotFound, "provider_not_found", "secret provider not found")
	ErrProviderExists    = httpx.Errorf(http.StatusConflict, "provider_exists", "a secret provider with this name exists")
	ErrProviderInUse     = httpx.Errorf(http.StatusConflict, "provider_in_use", "secrets use this provider")
	ErrProviderFixed     = httpx.Errorf(http.StatusConflict, "provider_fixed", "the builtin and env providers cannot change")
	ErrNamespaceNotFound = httpx.Errorf(http.StatusNotFound, "namespace_not_found", "namespace not found")
)

// Service manages providers and secrets and resolves secret references.
type Service struct {
	Pool  *pgxpool.Pool
	Clock clock.Clock
	Audit *audit.Writer
	Log   *slog.Logger
	Keys  *Keyring
	// CacheTTL is the cache lifetime of external values. Default DefaultCacheTTL.
	CacheTTL time.Duration
	// Lookup reads the process environment for the env provider. Default os.LookupEnv.
	Lookup func(string) (string, bool)
	Vault  VaultAuth
	// K8sKubeconfig and K8sNamespace configure the kubernetes provider.
	K8sKubeconfig string
	K8sNamespace  string
	// Azure creates the credential of the azure_key_vault provider. Default DefaultAzureCredential.
	Azure AzureFactory

	mu        sync.Mutex
	values    map[string]cachedValue
	providers map[uuid.UUID]cachedProvider
}

type cachedValue struct {
	value string
	at    time.Time
}

type cachedProvider struct {
	p     Provider
	stamp time.Time
}

func (s *Service) lookup() func(string) (string, bool) {
	if s.Lookup != nil {
		return s.Lookup
	}
	return os.LookupEnv
}

func (s *Service) ttl() time.Duration {
	if s.CacheTTL > 0 {
		return s.CacheTTL
	}
	return DefaultCacheTTL
}

func (s *Service) azure() AzureFactory {
	if s.Azure != nil {
		return s.Azure
	}
	return func() (azcore.TokenCredential, *azsecrets.ClientOptions, error) {
		cred, err := azidentity.NewDefaultAzureCredential(nil)
		return cred, nil, err
	}
}

// scopeLabel returns "global" for the global scope and the namespace name otherwise.
func scopeLabel(scope string) string {
	if scope == "" {
		return "global"
	}
	return scope
}

func actor(ctx context.Context) string {
	if p := kernel.FromContext(ctx); p != nil {
		return p.Email
	}
	return "system"
}

// namespaceID returns nil for the global scope and the ID of an existing namespace otherwise.
func namespaceID(ctx context.Context, q *secretdb.Queries, scope string) (*uuid.UUID, error) {
	if scope == "" {
		return nil, nil
	}
	id, err := q.NamespaceID(ctx, scope)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNamespaceNotFound
	}
	if err != nil {
		return nil, err
	}
	return &id, nil
}

// EnsureDefaults creates the builtin and env providers, which always exist (REQ-SEC-001).
func (s *Service) EnsureDefaults(ctx context.Context) error {
	q := secretdb.New(s.Pool)
	for _, t := range []string{TypeBuiltin, TypeEnv} {
		id, _ := uuid.NewV7()
		if err := q.EnsureProvider(ctx, secretdb.EnsureProviderParams{ID: id, Name: t, Type: t}); err != nil {
			return err
		}
	}
	return nil
}

// ProviderInfo is one provider.
type ProviderInfo struct {
	Name      string
	Type      string
	Config    map[string]any
	CreatedAt time.Time
	UpdatedAt time.Time
}

func providerInfo(p secretdb.SecretProvider) ProviderInfo {
	cfg := map[string]any{}
	_ = json.Unmarshal(p.Config, &cfg)
	return ProviderInfo{Name: p.Name, Type: p.Type, Config: cfg, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt}
}

// Providers lists the providers.
func (s *Service) Providers(ctx context.Context) ([]ProviderInfo, error) {
	rows, err := secretdb.New(s.Pool).ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ProviderInfo, 0, len(rows))
	for _, r := range rows {
		out = append(out, providerInfo(r))
	}
	return out, nil
}

func configError(err error) error {
	return httpx.Validation(httpx.FieldError{Field: "config", Message: err.Error()})
}

// CreateProvider adds a kubernetes, azure_key_vault or vault provider.
func (s *Service) CreateProvider(ctx context.Context, name, typ string, config map[string]any) (ProviderInfo, error) {
	if typ == TypeBuiltin || typ == TypeEnv {
		return ProviderInfo{}, httpx.Validation(httpx.FieldError{Field: "type", Message: "the builtin and env providers always exist"})
	}
	cfg, err := ParseProviderConfig(typ, config)
	if err != nil {
		return ProviderInfo{}, configError(err)
	}
	raw, _ := json.Marshal(cfg)
	var out ProviderInfo
	err = pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := secretdb.New(tx)
		if _, err := q.GetProvider(ctx, name); err == nil {
			return ErrProviderExists
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		id, _ := uuid.NewV7()
		p, err := q.InsertProvider(ctx, secretdb.InsertProviderParams{ID: id, Name: name, Type: typ, Config: raw, Now: s.Clock.Now()})
		if err != nil {
			return err
		}
		out = providerInfo(p)
		return s.Audit.Record(ctx, tx, audit.Event{Action: "secret_provider.create", TargetType: "secret_provider", TargetID: name,
			Details: map[string]any{"type": typ}})
	})
	return out, err
}

// UpdateProvider replaces the configuration of a provider.
func (s *Service) UpdateProvider(ctx context.Context, name string, config map[string]any) (ProviderInfo, error) {
	var out ProviderInfo
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := secretdb.New(tx)
		p, err := q.GetProvider(ctx, name)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrProviderNotFound
		}
		if err != nil {
			return err
		}
		if p.Type == TypeBuiltin || p.Type == TypeEnv {
			return ErrProviderFixed
		}
		cfg, err := ParseProviderConfig(p.Type, config)
		if err != nil {
			return configError(err)
		}
		raw, _ := json.Marshal(cfg)
		if p, err = q.UpdateProviderConfig(ctx, secretdb.UpdateProviderConfigParams{Config: raw, Now: s.Clock.Now(), Name: name}); err != nil {
			return err
		}
		out = providerInfo(p)
		return s.Audit.Record(ctx, tx, audit.Event{Action: "secret_provider.update", TargetType: "secret_provider", TargetID: name})
	})
	return out, err
}

// DeleteProvider removes a provider that no secret uses.
func (s *Service) DeleteProvider(ctx context.Context, name string) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := secretdb.New(tx)
		p, err := q.GetProvider(ctx, name)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrProviderNotFound
		}
		if err != nil {
			return err
		}
		if p.Type == TypeBuiltin || p.Type == TypeEnv {
			return ErrProviderFixed
		}
		inUse, err := q.ProviderInUse(ctx, p.ID)
		if err != nil {
			return err
		}
		if inUse {
			return ErrProviderInUse
		}
		if err := q.DeleteProvider(ctx, p.ID); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, audit.Event{Action: "secret_provider.delete", TargetType: "secret_provider", TargetID: name})
	})
}

// CheckProvider resolves a reference with a provider and reports the status (REQ-SEC-004).
func (s *Service) CheckProvider(ctx context.Context, name, ref string) (string, string, error) {
	p, err := secretdb.New(s.Pool).GetProvider(ctx, name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrProviderNotFound
	}
	if err != nil {
		return "", "", err
	}
	if p.Type == TypeBuiltin {
		return "", "", httpx.Validation(httpx.FieldError{Field: "ref", Message: "builtin secrets have no reference"})
	}
	prov, err := s.providerFor(ctx, p.ID, p.Type, p.Config, p.UpdatedAt)
	if err == nil {
		_, err = prov.Resolve(ctx, ref)
	}
	st, msg := StatusOf(err)
	return st, msg, nil
}

// StatusOf maps a resolution error to a check status and message (REQ-SEC-004). The
// message never holds a value.
func StatusOf(err error) (string, string) {
	switch {
	case err == nil:
		return "ok", ""
	case errors.Is(err, ErrValueNotFound):
		return "not_found", err.Error()
	case errors.Is(err, ErrAccessDenied):
		return "access_denied", err.Error()
	}
	return "provider_error", err.Error()
}

func (s *Service) providerFor(ctx context.Context, id uuid.UUID, typ string, raw json.RawMessage, stamp time.Time) (Provider, error) {
	s.mu.Lock()
	if c, ok := s.providers[id]; ok && c.stamp.Equal(stamp) {
		s.mu.Unlock()
		return c.p, nil
	}
	s.mu.Unlock()
	m := map[string]any{}
	_ = json.Unmarshal(raw, &m)
	cfg, err := ParseProviderConfig(typ, m)
	if err != nil {
		return nil, err
	}
	var p Provider
	switch typ {
	case TypeEnv:
		p = envProvider{lookup: s.lookup()}
	case TypeVault:
		vp, err := newVaultProvider(ctx, cfg, s.Vault)
		if err != nil {
			return nil, err
		}
		p = vp
	case TypeAzureKeyVault:
		ap, err := newAzureProvider(cfg, s.azure())
		if err != nil {
			return nil, err
		}
		p = ap
	case TypeKubernetes:
		kp, err := newKubernetesProvider(cfg, s.K8sKubeconfig, s.K8sNamespace)
		if err != nil {
			return nil, err
		}
		p = kp
	default:
		return nil, fmt.Errorf("provider type %s has no references", typ)
	}
	s.mu.Lock()
	if s.providers == nil {
		s.providers = map[uuid.UUID]cachedProvider{}
	}
	s.providers[id] = cachedProvider{p: p, stamp: stamp}
	s.mu.Unlock()
	return p, nil
}

// candidate is one stored definition of a secret key.
type candidate struct {
	id         uuid.UUID
	scope, key string
	ref        string
	ciphertext []byte
	keyID      string
	providerID uuid.UUID
	provider   string
	typ        string
	config     json.RawMessage
	stamp      time.Time
}

// value returns the plain value of a definition. External values are cached for the
// cache lifetime (REQ-SEC-005).
func (s *Service) value(ctx context.Context, c candidate) (string, error) {
	if c.typ == TypeBuiltin {
		b, err := s.Keys.Decrypt(c.keyID, c.ciphertext, AAD(c.scope, c.key))
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	ck := c.providerID.String() + "|" + c.stamp.UTC().Format(time.RFC3339Nano) + "|" + c.ref
	now := time.Now()
	if c.typ != TypeEnv {
		s.mu.Lock()
		if v, ok := s.values[ck]; ok && now.Sub(v.at) < s.ttl() {
			s.mu.Unlock()
			return v.value, nil
		}
		s.mu.Unlock()
	}
	p, err := s.providerFor(ctx, c.providerID, c.typ, c.config, c.stamp)
	if err != nil {
		return "", err
	}
	v, err := p.Resolve(ctx, c.ref)
	if err != nil {
		return "", err
	}
	if c.typ != TypeEnv {
		s.mu.Lock()
		if s.values == nil || len(s.values) > 10000 {
			s.values = map[string]cachedValue{}
		}
		s.values[ck] = cachedValue{value: v, at: now}
		s.mu.Unlock()
	}
	return v, nil
}

// NotFoundError is returned when no scope defines the key (REQ-SEC-009).
type NotFoundError struct {
	Key    string
	Scopes []string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("secret %q not found in scopes %s", e.Key, strings.Join(e.Scopes, ", "))
}

// ResolveError is returned when the provider of a defined key fails.
type ResolveError struct {
	Key, Scope, Provider string
	Err                  error
}

func (e *ResolveError) Error() string {
	return fmt.Sprintf("secret %q of scope %s (provider %s): %v", e.Key, scopeLabel(e.Scope), e.Provider, e.Err)
}

func (e *ResolveError) Unwrap() error { return e.Err }

// Resolve returns the value of key for a task in namespace: the namespace, then each
// parent, then global. The nearest definition wins (REQ-SEC-003).
func (s *Service) Resolve(ctx context.Context, namespace, key string) (string, error) {
	chain := flow.NamespaceChain(namespace)
	q := secretdb.New(s.Pool)
	rows, err := q.ResolveCandidates(ctx, secretdb.ResolveCandidatesParams{Key: key, Chain: chain})
	if err != nil {
		return "", err
	}
	best, rank := -1, len(chain)+1
	for i, r := range rows {
		if rk := rankOf(r.Scope, chain); rk < rank {
			best, rank = i, rk
		}
	}
	if best < 0 {
		return "", &NotFoundError{Key: key, Scopes: append(chain, "global")}
	}
	r := rows[best]
	c := candidate{id: r.ID, scope: r.Scope, key: key, ref: r.ProviderRef, ciphertext: r.Ciphertext, keyID: r.KeyID, providerID: r.ProviderID,
		provider: r.Provider, typ: r.ProviderType, config: r.ProviderConfig, stamp: r.ProviderUpdatedAt}
	v, err := s.value(ctx, c)
	if err != nil {
		return "", &ResolveError{Key: key, Scope: r.Scope, Provider: r.Provider, Err: err}
	}
	now := s.Clock.Now()
	if err := q.TouchSecret(ctx, secretdb.TouchSecretParams{Now: &now, ID: r.ID}); err != nil && s.Log != nil {
		s.Log.Warn("record secret use", "err", err)
	}
	return v, nil
}

// rankOf returns the position of a scope in the chain. The global scope ("") is last.
func rankOf(scope string, chain []string) int {
	if scope == "" {
		return len(chain)
	}
	for i, n := range chain {
		if n == scope {
			return i
		}
	}
	return len(chain) + 1
}

// Info is one secret without its value.
type Info struct {
	Key            string
	Scope          string
	Provider       string
	ProviderType   string
	Ref            string
	Description    string
	UpdatedBy      string
	UpdatedAt      time.Time
	LastResolvedAt *time.Time
	Inherited      bool
}

// List returns the global secrets (namespace "") or the effective secrets of a namespace:
// for each key the nearest definition, with the scope it comes from (REQ-UI-008).
func (s *Service) List(ctx context.Context, namespace string) ([]Info, error) {
	q := secretdb.New(s.Pool)
	if _, err := namespaceID(ctx, q, namespace); err != nil {
		return nil, err
	}
	chain := flow.NamespaceChain(namespace)
	rows, err := q.ListSecrets(ctx, chain)
	if err != nil {
		return nil, err
	}
	best := map[string]int{}
	var keys []string
	for i, r := range rows {
		cur, ok := best[r.Key]
		if !ok {
			keys = append(keys, r.Key)
		}
		if !ok || rankOf(r.Scope, chain) < rankOf(rows[cur].Scope, chain) {
			best[r.Key] = i
		}
	}
	out := make([]Info, 0, len(keys))
	for _, k := range keys {
		r := rows[best[k]]
		info := Info{Key: r.Key, Scope: scopeLabel(r.Scope), Provider: r.Provider, ProviderType: r.ProviderType, Description: r.Description,
			UpdatedBy: r.UpdatedBy, UpdatedAt: r.UpdatedAt, LastResolvedAt: r.LastResolvedAt, Inherited: r.Scope != namespace}
		if r.ProviderType != TypeBuiltin {
			info.Ref = r.ProviderRef
		}
		out = append(out, info)
	}
	return out, nil
}

// PutInput is a secret write. Value is for builtin secrets, Ref for external ones.
type PutInput struct {
	Provider    string
	Value       *string
	Ref         string
	Description *string
}

// Put creates or updates a secret in a scope ("" is global). Values are write-only
// (REQ-SEC-002). It returns whether the secret is new.
func (s *Service) Put(ctx context.Context, scope, key string, in PutInput) (bool, error) {
	var created bool
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := secretdb.New(tx)
		nsID, err := namespaceID(ctx, q, scope)
		if err != nil {
			return err
		}
		existing, err := q.GetSecret(ctx, secretdb.GetSecretParams{NamespaceID: nsID, Key: key})
		exists := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		name := in.Provider
		if name == "" {
			name = TypeBuiltin
			if exists {
				name = existing.Provider
			}
		}
		prov, err := q.GetProvider(ctx, name)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.Validation(httpx.FieldError{Field: "provider", Message: fmt.Sprintf("unknown provider %q", name)})
		}
		if err != nil {
			return err
		}
		sameProvider := exists && existing.ProviderID == prov.ID
		params := secretdb.UpsertSecretParams{NamespaceID: nsID, Key: key, ProviderID: prov.ID, Actor: actor(ctx), Now: s.Clock.Now()}
		params.ID, _ = uuid.NewV7()
		switch prov.Type {
		case TypeBuiltin:
			if !s.Keys.Enabled() {
				return ErrBuiltinDisabled
			}
			switch {
			case in.Value != nil:
				if params.Ciphertext, params.KeyID, err = s.Keys.Encrypt([]byte(*in.Value), AAD(scope, key)); err != nil {
					return err
				}
			case sameProvider:
				params.Ciphertext, params.KeyID = existing.Ciphertext, existing.KeyID
			default:
				return httpx.Validation(httpx.FieldError{Field: "value", Message: "a builtin secret needs a value"})
			}
		default:
			if in.Value != nil {
				return httpx.Validation(httpx.FieldError{Field: "value", Message: "an external secret stores only a reference"})
			}
			params.ProviderRef = in.Ref
			if params.ProviderRef == "" && sameProvider {
				params.ProviderRef = existing.ProviderRef
			}
			if params.ProviderRef == "" && prov.Type == TypeEnv {
				params.ProviderRef = key
			}
			if params.ProviderRef == "" {
				return httpx.Validation(httpx.FieldError{Field: "ref", Message: "an external secret needs a reference"})
			}
		}
		switch {
		case in.Description != nil:
			params.Description = *in.Description
		case exists:
			params.Description = existing.Description
		}
		if created, err = q.UpsertSecret(ctx, params); err != nil {
			return err
		}
		action := "secret.update"
		if created {
			action = "secret.create"
		}
		return s.Audit.Record(ctx, tx, audit.Event{Action: action, TargetType: "secret", TargetID: AAD(scope, key),
			Details: map[string]any{"provider": name}})
	})
	return created, err
}

// Get returns one secret of a scope without its value.
func (s *Service) Get(ctx context.Context, scope, key string) (Info, error) {
	list, err := s.List(ctx, scope)
	if err != nil {
		return Info{}, err
	}
	for _, i := range list {
		if i.Key == key && i.Scope == scopeLabel(scope) {
			return i, nil
		}
	}
	return Info{}, ErrSecretNotFound
}

// Delete removes a secret from a scope.
func (s *Service) Delete(ctx context.Context, scope, key string) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := secretdb.New(tx)
		nsID, err := namespaceID(ctx, q, scope)
		if err != nil {
			return err
		}
		n, err := q.DeleteSecret(ctx, secretdb.DeleteSecretParams{NamespaceID: nsID, Key: key})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrSecretNotFound
		}
		return s.Audit.Record(ctx, tx, audit.Event{Action: "secret.delete", TargetType: "secret", TargetID: AAD(scope, key)})
	})
}

// Check resolves a secret of a scope and reports the status without the value (REQ-SEC-004).
func (s *Service) Check(ctx context.Context, scope, key string) (string, string, error) {
	q := secretdb.New(s.Pool)
	nsID, err := namespaceID(ctx, q, scope)
	if err != nil {
		return "", "", err
	}
	r, err := q.GetSecret(ctx, secretdb.GetSecretParams{NamespaceID: nsID, Key: key})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrSecretNotFound
	}
	if err != nil {
		return "", "", err
	}
	_, err = s.value(ctx, candidate{id: r.ID, scope: scope, key: key, ref: r.ProviderRef, ciphertext: r.Ciphertext, keyID: r.KeyID,
		providerID: r.ProviderID, provider: r.Provider, typ: r.ProviderType, config: r.ProviderConfig, stamp: r.ProviderUpdatedAt})
	st, msg := StatusOf(err)
	return st, msg, nil
}

// Rekey encrypts every builtin secret with the active master key (REQ-SEC-006). It
// returns the number of secrets that changed.
func (s *Service) Rekey(ctx context.Context) (int, error) {
	if !s.Keys.Enabled() {
		return 0, ErrNoMasterKey
	}
	n := 0
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := secretdb.New(tx)
		rows, err := q.ListBuiltinSecrets(ctx)
		if err != nil {
			return err
		}
		for _, r := range rows {
			if r.KeyID == s.Keys.ActiveID() {
				continue
			}
			plain, err := s.Keys.Decrypt(r.KeyID, r.Ciphertext, AAD(r.Scope, r.Key))
			if err != nil {
				return fmt.Errorf("secret %s: %w", AAD(r.Scope, r.Key), err)
			}
			sealed, kid, err := s.Keys.Encrypt(plain, AAD(r.Scope, r.Key))
			if err != nil {
				return err
			}
			if err := q.SetCiphertext(ctx, secretdb.SetCiphertextParams{Ciphertext: sealed, KeyID: kid, ID: r.ID}); err != nil {
				return err
			}
			n++
		}
		return s.Audit.Record(ctx, tx, audit.Event{Action: "secret.rekey", TargetType: "secret", TargetID: "*",
			Details: map[string]any{"count": n, "key_id": s.Keys.ActiveID()}})
	})
	return n, err
}

// CheckKeys fails with master_key_missing when a stored key ID has no master key. /readyz uses it (REQ-SEC-006).
func (s *Service) CheckKeys(ctx context.Context) error {
	ids, err := secretdb.New(s.Pool).StoredKeyIDs(ctx)
	if err != nil {
		return err
	}
	var missing []string
	for _, id := range ids {
		if !s.Keys.Has(id) {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("master_key_missing: no master key for key IDs %s", strings.Join(missing, ", "))
	}
	return nil
}
