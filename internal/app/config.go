// Package app holds configuration, wiring and lifecycle of the sluice binary.
package app

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ByteSize is a size in bytes. It parses values like 10MiB, 512KiB or 1024.
type ByteSize int64

// Config holds all server configuration. Every field comes from one environment
// variable (C-04). The struct tags are the single source for docs/reference/env.md.
type Config struct {
	DatabaseURL            string        `env:"SLUICE_DATABASE_URL" required:"always" desc:"Postgres URL. A pooled URL (PgBouncer transaction mode, Neon pooler) is allowed."`
	ListenAddr             string        `env:"SLUICE_LISTEN_ADDR" default:":8080" desc:"HTTP listen address."`
	PublicURL              string        `env:"SLUICE_PUBLIC_URL" required:"server" desc:"External base URL for links, cookies and webhooks. An https URL makes the session cookie Secure."`
	InternalURL            string        `env:"SLUICE_INTERNAL_URL" default:"http://127.0.0.1:<port>" desc:"Base URL that runners call. Use the Service URL in Kubernetes."`
	MasterKeys             string        `env:"SLUICE_MASTER_KEYS" desc:"Master keys for builtin secrets as kid:base64key pairs separated by commas. The first key is active."`
	BootstrapAdminEmail    string        `env:"SLUICE_BOOTSTRAP_ADMIN_EMAIL" desc:"Email of the first admin. Used only when the users table is empty."`
	BootstrapAdminPassword string        `env:"SLUICE_BOOTSTRAP_ADMIN_PASSWORD" secret:"true" desc:"Password of the first admin. Used only when the users table is empty."`
	SessionTTL             time.Duration `env:"SLUICE_SESSION_TTL" default:"168h" desc:"Sliding session lifetime."`
	LogLevel               string        `env:"SLUICE_LOG_LEVEL" default:"info" enum:"debug,info,warn,error" desc:"Server log level."`
	LogFormat              string        `env:"SLUICE_LOG_FORMAT" default:"json" enum:"json,text" desc:"Server log format."`
	Pools                  []string      `env:"SLUICE_POOLS" default:"default" desc:"Comma-separated pools that this instance serves."`
	Executors              []string      `env:"SLUICE_EXECUTORS" default:"auto" desc:"auto, or a comma-separated list of process, docker and kubernetes. inline is always enabled."`
	WorkerSlots            int           `env:"SLUICE_WORKER_SLOTS" default:"8" min:"0" desc:"Slots for process and docker tasks on this instance."`
	QueuePollInterval      time.Duration `env:"SLUICE_QUEUE_POLL_INTERVAL" default:"1s" desc:"Interval between queue claim polls."`
	HeartbeatTimeout       time.Duration `env:"SLUICE_HEARTBEAT_TIMEOUT" default:"60s" desc:"Time without runner heartbeat after which a task run is checked and can become lost."`
	ShutdownGrace          time.Duration `env:"SLUICE_SHUTDOWN_GRACE" default:"30s" desc:"Maximum time between SIGTERM and process exit."`
	RetentionDays          int           `env:"SLUICE_RETENTION_DAYS" default:"90" min:"1" desc:"Days to keep ended executions with their logs, metrics and artifacts."`
	StorageType            string        `env:"SLUICE_STORAGE_TYPE" default:"postgres" enum:"postgres,fs,s3,azblob" desc:"Object storage driver."`
	FSRoot                 string        `env:"SLUICE_FS_ROOT" desc:"Root directory of the fs storage driver."`
	S3Bucket               string        `env:"SLUICE_S3_BUCKET" desc:"Bucket of the s3 storage driver."`
	S3Region               string        `env:"SLUICE_S3_REGION" desc:"Region of the s3 storage driver."`
	S3Endpoint             string        `env:"SLUICE_S3_ENDPOINT" desc:"Endpoint override of the s3 storage driver (Cloudflare R2, MinIO)."`
	S3ForcePathStyle       bool          `env:"SLUICE_S3_FORCE_PATH_STYLE" default:"false" desc:"Use path-style addressing in the s3 storage driver."`
	S3AccessKeyID          string        `env:"SLUICE_S3_ACCESS_KEY_ID" desc:"Static access key ID. Empty uses the default AWS credential chain."`
	S3SecretAccessKey      string        `env:"SLUICE_S3_SECRET_ACCESS_KEY" secret:"true" desc:"Static secret access key. Empty uses the default AWS credential chain."`
	S3Prefix               string        `env:"SLUICE_S3_PREFIX" desc:"Key prefix in the s3 bucket."`
	AzblobAccountURL       string        `env:"SLUICE_AZBLOB_ACCOUNT_URL" desc:"Account URL of the azblob storage driver. Uses DefaultAzureCredential."`
	AzblobContainer        string        `env:"SLUICE_AZBLOB_CONTAINER" desc:"Container of the azblob storage driver."`
	AzblobConnectionString string        `env:"SLUICE_AZBLOB_CONNECTION_STRING" secret:"true" desc:"Connection string of the azblob storage driver. Used instead of the account URL."`
	AzblobPrefix           string        `env:"SLUICE_AZBLOB_PREFIX" desc:"Key prefix in the azblob container."`
	MaxFileBytes           ByteSize      `env:"SLUICE_MAX_FILE_BYTES" default:"10MiB" desc:"Maximum size of one namespace file."`
	MaxBundleBytes         ByteSize      `env:"SLUICE_MAX_BUNDLE_BYTES" default:"200MiB" desc:"Maximum total size of one snapshot."`
	MaxArtifactBytes       ByteSize      `env:"SLUICE_MAX_ARTIFACT_BYTES" default:"100MiB" desc:"Maximum size of one artifact."`
	RunnerImage            string        `env:"SLUICE_RUNNER_IMAGE" default:"sluice:<version>" desc:"Image that holds the runner binary for injection into docker and kubernetes tasks."`
	DockerAPIURL           string        `env:"SLUICE_DOCKER_API_URL" default:"http://host.docker.internal:<port>" desc:"Base URL that runners in docker containers call."`
	DockerKeepContainers   bool          `env:"SLUICE_DOCKER_KEEP_CONTAINERS" default:"false" desc:"Keep docker task containers after completion."`
	K8sKubeconfig          string        `env:"SLUICE_K8S_KUBECONFIG" desc:"Path to a kubeconfig for out-of-cluster access."`
	K8sNamespace           string        `env:"SLUICE_K8S_NAMESPACE" default:"<own namespace>" desc:"Kubernetes namespace for task Jobs. Default is the namespace of the server pod."`
	K8sMaxJobs             int           `env:"SLUICE_K8S_MAX_JOBS" default:"50" min:"1" desc:"Maximum running Jobs per pool."`
	K8sJobTTL              time.Duration `env:"SLUICE_K8S_JOB_TTL" default:"600s" desc:"ttlSecondsAfterFinished of task Jobs."`
	K8sPendingTimeout      time.Duration `env:"SLUICE_K8S_PENDING_TIMEOUT" default:"10m" desc:"Maximum time a task pod can stay pending."`
	SecretCacheTTL         time.Duration `env:"SLUICE_SECRET_CACHE_TTL" default:"60s" desc:"Cache lifetime of external secret values."`
	VaultAddr              string        `env:"SLUICE_VAULT_ADDR" desc:"HashiCorp Vault address."`
	VaultToken             string        `env:"SLUICE_VAULT_TOKEN" secret:"true" desc:"HashiCorp Vault token."`
	VaultK8sRole           string        `env:"SLUICE_VAULT_K8S_ROLE" desc:"HashiCorp Vault Kubernetes auth role. Used when no token is set."`
	AIMaxContextChars      int           `env:"SLUICE_AI_MAX_CONTEXT_CHARS" default:"120000" min:"1000" desc:"Maximum characters of model context."`
}

// SecretEnvPrefix is the prefix of variables that hold values for the env secret provider.
const SecretEnvPrefix = "SLUICE_SECRET_"

// Requirement levels for the `required` tag.
const (
	requiredAlways = "always"
	requiredServer = "server"
)

// ConfigError lists all configuration errors. Startup stops with exit code 2.
type ConfigError struct{ Problems []string }

func (e *ConfigError) Error() string {
	return "invalid configuration:\n  " + strings.Join(e.Problems, "\n  ")
}

// LoadOptions selects which requirement levels apply.
type LoadOptions struct {
	Server  bool
	Version string
	Getenv  func(string) string
}

// LoadConfig reads the configuration from the environment and validates it.
func LoadConfig(opts LoadOptions) (*Config, error) {
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	cfg := &Config{}
	var problems []string
	v := reflect.ValueOf(cfg).Elem()
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name := f.Tag.Get("env")
		raw := strings.TrimSpace(getenv(name))
		req := f.Tag.Get("required")
		if raw == "" {
			if req == requiredAlways || (req == requiredServer && opts.Server) {
				problems = append(problems, fmt.Sprintf("%s: required", name))
				continue
			}
			raw = f.Tag.Get("default")
			if strings.Contains(raw, "<") {
				continue // computed default, see applyComputedDefaults
			}
		}
		if raw == "" {
			continue
		}
		if err := setField(v.Field(i), f, raw); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", name, err))
		}
	}
	problems = append(problems, cfg.validate()...)
	if len(problems) > 0 {
		return nil, &ConfigError{Problems: problems}
	}
	cfg.applyComputedDefaults(opts.Version)
	return cfg, nil
}

func setField(fv reflect.Value, f reflect.StructField, raw string) error {
	if enum := f.Tag.Get("enum"); enum != "" {
		if !contains(strings.Split(enum, ","), raw) {
			return fmt.Errorf("must be one of %s, got %q", enum, raw)
		}
	}
	switch fv.Interface().(type) {
	case string:
		fv.SetString(raw)
	case bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("must be true or false, got %q", raw)
		}
		fv.SetBool(b)
	case int:
		n, err := strconv.Atoi(raw)
		if err != nil {
			return fmt.Errorf("must be an integer, got %q", raw)
		}
		if m := f.Tag.Get("min"); m != "" {
			minV, _ := strconv.Atoi(m)
			if n < minV {
				return fmt.Errorf("must be at least %d, got %d", minV, n)
			}
		}
		fv.SetInt(int64(n))
	case time.Duration:
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			return fmt.Errorf("must be a positive duration like 30s, got %q", raw)
		}
		fv.SetInt(int64(d))
	case ByteSize:
		n, err := ParseByteSize(raw)
		if err != nil {
			return err
		}
		fv.SetInt(n)
	case []string:
		var out []string
		for _, p := range strings.Split(raw, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		fv.Set(reflect.ValueOf(out))
	default:
		return fmt.Errorf("unsupported field type %s", fv.Type())
	}
	return nil
}

// ParseByteSize parses 1024, 512KiB, 10MiB, 1GiB (and KB, MB, GB as the same binary units).
func ParseByteSize(raw string) (int64, error) {
	s := strings.TrimSpace(raw)
	units := []struct {
		suffix string
		mult   int64
	}{{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}, {"GB", 1 << 30}, {"MB", 1 << 20}, {"KB", 1 << 10}, {"B", 1}}
	mult := int64(1)
	for _, u := range units {
		if strings.HasSuffix(s, u.suffix) {
			s = strings.TrimSpace(strings.TrimSuffix(s, u.suffix))
			mult = u.mult
			break
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("must be a positive size like 10MiB, got %q", raw)
	}
	return n * mult, nil
}

func (c *Config) validate() []string {
	var p []string
	if c.PublicURL != "" {
		if u, err := url.Parse(c.PublicURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			p = append(p, "SLUICE_PUBLIC_URL: must be an absolute http or https URL")
		}
	}
	if c.InternalURL != "" {
		if u, err := url.Parse(c.InternalURL); err != nil || u.Host == "" {
			p = append(p, "SLUICE_INTERNAL_URL: must be an absolute URL")
		}
	}
	if c.ListenAddr != "" {
		if _, _, err := net.SplitHostPort(c.ListenAddr); err != nil {
			p = append(p, "SLUICE_LISTEN_ADDR: must be host:port or :port")
		}
	}
	if c.MasterKeys != "" {
		if _, err := ParseMasterKeys(c.MasterKeys); err != nil {
			p = append(p, "SLUICE_MASTER_KEYS: "+err.Error())
		}
	}
	for _, e := range c.Executors {
		if !contains([]string{"auto", "inline", "process", "docker", "kubernetes"}, e) {
			p = append(p, fmt.Sprintf("SLUICE_EXECUTORS: unknown executor %q", e))
		}
	}
	if contains(c.Executors, "auto") && len(c.Executors) > 1 {
		p = append(p, "SLUICE_EXECUTORS: auto cannot be combined with other values")
	}
	for _, pool := range c.Pools {
		if !validPoolName(pool) {
			p = append(p, fmt.Sprintf("SLUICE_POOLS: invalid pool name %q", pool))
		}
	}
	switch c.StorageType {
	case "fs":
		if c.FSRoot == "" {
			p = append(p, "SLUICE_FS_ROOT: required when SLUICE_STORAGE_TYPE is fs")
		}
	case "s3":
		if c.S3Bucket == "" {
			p = append(p, "SLUICE_S3_BUCKET: required when SLUICE_STORAGE_TYPE is s3")
		}
		if (c.S3AccessKeyID == "") != (c.S3SecretAccessKey == "") {
			p = append(p, "SLUICE_S3_ACCESS_KEY_ID and SLUICE_S3_SECRET_ACCESS_KEY: set both or neither")
		}
	case "azblob":
		if c.AzblobContainer == "" {
			p = append(p, "SLUICE_AZBLOB_CONTAINER: required when SLUICE_STORAGE_TYPE is azblob")
		}
		if c.AzblobAccountURL == "" && c.AzblobConnectionString == "" {
			p = append(p, "SLUICE_AZBLOB_ACCOUNT_URL or SLUICE_AZBLOB_CONNECTION_STRING: one is required when SLUICE_STORAGE_TYPE is azblob")
		}
	}
	return p
}

func validPoolName(s string) bool {
	if s == "" || len(s) > 63 {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

// Port returns the port of the listen address.
func (c *Config) Port() string {
	_, port, err := net.SplitHostPort(c.ListenAddr)
	if err != nil || port == "" {
		return "8080"
	}
	return port
}

func (c *Config) applyComputedDefaults(version string) {
	if c.InternalURL == "" {
		c.InternalURL = "http://127.0.0.1:" + c.Port()
	}
	if c.DockerAPIURL == "" {
		c.DockerAPIURL = "http://host.docker.internal:" + c.Port()
	}
	if c.RunnerImage == "" {
		if version == "" {
			version = "dev"
		}
		c.RunnerImage = "sluice:" + version
	}
	if c.K8sNamespace == "" {
		if b, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil {
			c.K8sNamespace = strings.TrimSpace(string(b))
		} else {
			c.K8sNamespace = "default"
		}
	}
}

// SecureCookies reports whether the public URL uses https.
func (c *Config) SecureCookies() bool {
	return strings.HasPrefix(strings.ToLower(c.PublicURL), "https://")
}

// MasterKey is one entry of SLUICE_MASTER_KEYS.
type MasterKey struct {
	ID  string
	Key []byte
}

// ParseMasterKeys parses kid:base64key pairs. Each key must decode to 32 bytes.
func ParseMasterKeys(raw string) ([]MasterKey, error) {
	var keys []MasterKey
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kid, b64, ok := strings.Cut(part, ":")
		if !ok || kid == "" {
			return nil, errors.New("each entry must be kid:base64key")
		}
		if seen[kid] {
			return nil, fmt.Errorf("duplicate key ID %q", kid)
		}
		seen[kid] = true
		key, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return nil, fmt.Errorf("key %q is not valid base64", kid)
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("key %q must be 32 bytes, got %d", kid, len(key))
		}
		keys = append(keys, MasterKey{ID: kid, Key: key})
	}
	return keys, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// EnvDoc renders docs/reference/env.md from the Config struct tags (REQ-DOC-001).
func EnvDoc() (string, error) {
	var b strings.Builder
	b.WriteString("# Environment variables\n\n")
	b.WriteString("Generated from `internal/app/config.go` by `just gen`. Do not edit.\n\n")
	b.WriteString("| Variable | Default | Description |\n|---|---|---|\n")
	t := reflect.TypeOf(Config{})
	var missing []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name := f.Tag.Get("env")
		desc := f.Tag.Get("desc")
		if desc == "" {
			missing = append(missing, name)
		}
		def := f.Tag.Get("default")
		switch f.Tag.Get("required") {
		case requiredAlways:
			def = "required"
		case requiredServer:
			def = "required for `server`"
		}
		if def == "" {
			def = "empty"
		} else if def != "required" && def != "required for `server`" {
			def = "`" + def + "`"
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", name, def, desc)
	}
	fmt.Fprintf(&b, "| `%s<KEY>` | empty | Value of secret `<KEY>` for the env secret provider. |\n", SecretEnvPrefix)
	b.WriteString("\nAzure credentials use the standard `AZURE_*` variables, workload identity or managed identity.\n")
	if len(missing) > 0 {
		sort.Strings(missing)
		return "", fmt.Errorf("config fields without description: %s", strings.Join(missing, ", "))
	}
	return b.String(), nil
}
