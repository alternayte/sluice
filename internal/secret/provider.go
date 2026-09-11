package secret

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
	vault "github.com/hashicorp/vault/api"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Provider types (REQ-SEC-001).
const (
	TypeBuiltin       = "builtin"
	TypeEnv           = "env"
	TypeKubernetes    = "kubernetes"
	TypeAzureKeyVault = "azure_key_vault"
	TypeVault         = "vault"
)

// Errors that a provider returns. Check maps them to not_found and access_denied (REQ-SEC-004).
var (
	ErrValueNotFound = errors.New("not found")
	ErrAccessDenied  = errors.New("access denied")
)

// EnvPrefix is the prefix of process variables that the env provider reads.
const EnvPrefix = "SLUICE_SECRET_"

// Provider resolves an external reference to a value.
type Provider interface {
	Resolve(ctx context.Context, ref string) (string, error)
}

// ProviderConfig is the stored configuration of a provider. It holds no credentials.
type ProviderConfig struct {
	// Namespace is the Kubernetes namespace of the kubernetes provider.
	Namespace string `json:"namespace,omitempty"`
	// VaultURL is the Key Vault URL of the azure_key_vault provider.
	VaultURL string `json:"vault_url,omitempty"`
	// Mount is the KV v2 mount of the vault provider. Default "secret".
	Mount string `json:"mount,omitempty"`
	// Address overrides SLUICE_VAULT_ADDR for the vault provider.
	Address string `json:"address,omitempty"`
}

// allowedConfig lists the configuration fields of each provider type.
var allowedConfig = map[string][]string{
	TypeBuiltin:       {},
	TypeEnv:           {},
	TypeKubernetes:    {"namespace"},
	TypeAzureKeyVault: {"vault_url"},
	TypeVault:         {"mount", "address"},
}

// ParseProviderConfig checks a provider configuration. Only the fields of the type are
// allowed, so that no credential can be stored (REQ-SEC-001).
func ParseProviderConfig(typ string, raw map[string]any) (ProviderConfig, error) {
	allowed, ok := allowedConfig[typ]
	if !ok {
		return ProviderConfig{}, fmt.Errorf("unknown provider type %q", typ)
	}
	for k := range raw {
		found := false
		for _, a := range allowed {
			found = found || a == k
		}
		if !found {
			return ProviderConfig{}, fmt.Errorf("field %q is not allowed for %s providers", k, typ)
		}
	}
	b, _ := json.Marshal(raw)
	var c ProviderConfig
	if err := json.Unmarshal(b, &c); err != nil {
		return ProviderConfig{}, fmt.Errorf("configuration fields must be strings")
	}
	switch typ {
	case TypeAzureKeyVault:
		if u, err := url.Parse(c.VaultURL); err != nil || u.Scheme != "https" || u.Host == "" {
			return c, fmt.Errorf("vault_url must be an https URL")
		}
	case TypeVault:
		if c.Address != "" {
			if u, err := url.Parse(c.Address); err != nil || u.Host == "" {
				return c, fmt.Errorf("address must be a URL")
			}
		}
	}
	return c, nil
}

// envProvider reads SLUICE_SECRET_<ref> from the process environment.
type envProvider struct {
	lookup func(string) (string, bool)
}

func (p envProvider) Resolve(_ context.Context, ref string) (string, error) {
	if v, ok := p.lookup(EnvPrefix + ref); ok {
		return v, nil
	}
	return "", fmt.Errorf("%w: %s%s is not set", ErrValueNotFound, EnvPrefix, ref)
}

// vaultProvider reads KV v2 values. A reference is `path#field`.
type vaultProvider struct {
	client *vault.Client
	mount  string
	// login gets a new token. It is nil for static-token auth, which is never renewed.
	login vaultLogin
	now   func() time.Time

	mu sync.Mutex
	// renewAt is the time of the next login, at 3/4 of the lease. Zero means no renewal.
	renewAt time.Time
}

// vaultLogin logs in to Vault and returns the auth response.
type vaultLogin func(ctx context.Context, client *vault.Client) (*vault.Secret, error)

// VaultAuth holds the Vault credentials from the environment (Appendix A).
type VaultAuth struct {
	Addr    string
	Token   string
	K8sRole string
	// K8sTokenPath is the service account token file for Kubernetes auth.
	K8sTokenPath string
}

func newVaultProvider(ctx context.Context, c ProviderConfig, auth VaultAuth) (*vaultProvider, error) {
	switch {
	case auth.Token != "":
		p, err := newVaultClient(c, auth.Addr, nil)
		if err != nil {
			return nil, err
		}
		p.client.SetToken(auth.Token)
		return p, nil
	case auth.K8sRole != "":
		return newVaultProviderLogin(ctx, c, auth.Addr, kubernetesLogin(auth))
	default:
		return nil, errors.New("no Vault credential: set SLUICE_VAULT_TOKEN or SLUICE_VAULT_K8S_ROLE")
	}
}

// newVaultProviderLogin returns a provider that gets its token from login. It logs in again
// before the lease ends and once after an access error (REQ-SEC-001).
func newVaultProviderLogin(ctx context.Context, c ProviderConfig, addr string, login vaultLogin) (*vaultProvider, error) {
	p, err := newVaultClient(c, addr, login)
	if err != nil {
		return nil, err
	}
	if err := p.relogin(ctx); err != nil {
		return nil, err
	}
	return p, nil
}

func newVaultClient(c ProviderConfig, addr string, login vaultLogin) (*vaultProvider, error) {
	cfg := vault.DefaultConfig()
	cfg.Address = addr
	if c.Address != "" {
		cfg.Address = c.Address
	}
	if cfg.Address == "" {
		return nil, errors.New("no Vault address: set SLUICE_VAULT_ADDR or the provider address")
	}
	cfg.MaxRetries = 1
	client, err := vault.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	mount := c.Mount
	if mount == "" {
		mount = "secret"
	}
	return &vaultProvider{client: client, mount: mount, login: login, now: time.Now}, nil
}

// kubernetesLogin logs in with the Kubernetes auth method. It reads the service account
// token file at each login, because the kubelet rotates the file.
func kubernetesLogin(auth VaultAuth) vaultLogin {
	path := auth.K8sTokenPath
	if path == "" {
		path = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	}
	return func(ctx context.Context, client *vault.Client) (*vault.Secret, error) {
		jwt, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return client.Logical().WriteWithContext(ctx, "auth/kubernetes/login", map[string]any{"role": auth.K8sRole, "jwt": string(jwt)})
	}
}

// relogin gets a new token and sets the next renewal time from the lease duration.
func (p *vaultProvider) relogin(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, err := p.login(ctx, p.client)
	if err != nil {
		return fmt.Errorf("vault kubernetes auth: %w", err)
	}
	if s == nil || s.Auth == nil || s.Auth.ClientToken == "" {
		return errors.New("vault kubernetes auth: no token")
	}
	p.client.SetToken(s.Auth.ClientToken)
	p.renewAt = time.Time{}
	if s.Auth.LeaseDuration > 0 {
		p.renewAt = p.now().Add(time.Duration(s.Auth.LeaseDuration) * time.Second * 3 / 4)
	}
	return nil
}

// renewDue reports whether the token lease is near its end.
func (p *vaultProvider) renewDue() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.renewAt.IsZero() && !p.now().Before(p.renewAt)
}

func (p *vaultProvider) Resolve(ctx context.Context, ref string) (string, error) {
	path, field, ok := strings.Cut(ref, "#")
	if !ok || path == "" || field == "" {
		return "", fmt.Errorf("%w: vault references have the form path#field", ErrValueNotFound)
	}
	if p.login != nil && p.renewDue() {
		if err := p.relogin(ctx); err != nil {
			return "", err
		}
	}
	s, err := p.client.KVv2(p.mount).Get(ctx, path)
	var re *vault.ResponseError
	if err != nil && p.login != nil && errors.As(err, &re) && (re.StatusCode == http.StatusForbidden || re.StatusCode == http.StatusUnauthorized) {
		// The token can be expired or revoked. Log in again once and read again.
		if lerr := p.relogin(ctx); lerr != nil {
			return "", lerr
		}
		s, err = p.client.KVv2(p.mount).Get(ctx, path)
	}
	if err != nil {
		switch {
		case errors.Is(err, vault.ErrSecretNotFound):
			return "", fmt.Errorf("%w: %s", ErrValueNotFound, path)
		case errors.As(err, &re) && re.StatusCode == http.StatusNotFound:
			return "", fmt.Errorf("%w: %s", ErrValueNotFound, path)
		case errors.As(err, &re) && (re.StatusCode == http.StatusForbidden || re.StatusCode == http.StatusUnauthorized):
			return "", fmt.Errorf("%w: %s", ErrAccessDenied, path)
		}
		return "", err
	}
	v, ok := s.Data[field]
	if !ok {
		return "", fmt.Errorf("%w: field %s of %s", ErrValueNotFound, field, path)
	}
	if str, ok := v.(string); ok {
		return str, nil
	}
	b, _ := json.Marshal(v)
	return string(b), nil
}

// AzureFactory creates the credential and client options of the azure_key_vault
// provider. The default uses DefaultAzureCredential (REQ-SEC-001).
type AzureFactory func() (azcore.TokenCredential, *azsecrets.ClientOptions, error)

// azureProvider reads Key Vault secrets. A reference is `name` or `name/version`.
type azureProvider struct {
	client *azsecrets.Client
}

func newAzureProvider(c ProviderConfig, factory AzureFactory) (*azureProvider, error) {
	cred, opts, err := factory()
	if err != nil {
		return nil, err
	}
	client, err := azsecrets.NewClient(c.VaultURL, cred, opts)
	if err != nil {
		return nil, err
	}
	return &azureProvider{client: client}, nil
}

func (p *azureProvider) Resolve(ctx context.Context, ref string) (string, error) {
	name, version, _ := strings.Cut(ref, "/")
	resp, err := p.client.GetSecret(ctx, name, version, nil)
	if err != nil {
		var re *azcore.ResponseError
		if errors.As(err, &re) {
			switch re.StatusCode {
			case http.StatusNotFound:
				return "", fmt.Errorf("%w: %s", ErrValueNotFound, ref)
			case http.StatusUnauthorized, http.StatusForbidden:
				return "", fmt.Errorf("%w: %s", ErrAccessDenied, ref)
			}
		}
		return "", err
	}
	if resp.Value == nil {
		return "", fmt.Errorf("%w: %s has no value", ErrValueNotFound, ref)
	}
	return *resp.Value, nil
}

// kubernetesProvider reads Secret keys. A reference is `secret-name/key`.
type kubernetesProvider struct {
	client    kubernetes.Interface
	namespace string
}

func newKubernetesProvider(c ProviderConfig, kubeconfig, defaultNamespace string) (*kubernetesProvider, error) {
	var cfg *rest.Config
	var err error
	if kubeconfig != "" {
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		cfg, err = rest.InClusterConfig()
	}
	if err != nil {
		return nil, fmt.Errorf("kubernetes config: %w", err)
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	ns := c.Namespace
	if ns == "" {
		ns = defaultNamespace
	}
	if ns == "" {
		ns = "default"
	}
	return &kubernetesProvider{client: client, namespace: ns}, nil
}

func (p *kubernetesProvider) Resolve(ctx context.Context, ref string) (string, error) {
	name, key, ok := strings.Cut(ref, "/")
	if !ok || name == "" || key == "" {
		return "", fmt.Errorf("%w: kubernetes references have the form secret-name/key", ErrValueNotFound)
	}
	s, err := p.client.CoreV1().Secrets(p.namespace).Get(ctx, name, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		return "", fmt.Errorf("%w: secret %s/%s", ErrValueNotFound, p.namespace, name)
	case apierrors.IsForbidden(err), apierrors.IsUnauthorized(err):
		return "", fmt.Errorf("%w: secret %s/%s", ErrAccessDenied, p.namespace, name)
	case err != nil:
		return "", err
	}
	v, ok := s.Data[key]
	if !ok {
		return "", fmt.Errorf("%w: key %s of secret %s/%s", ErrValueNotFound, key, p.namespace, name)
	}
	return string(v), nil
}
