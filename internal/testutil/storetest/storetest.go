// Package storetest starts MinIO and Azurite containers for storage tests (D-18).
package storetest

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/minio"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/alternayte/sluice/internal/storage"
)

// MinIOServer is a running MinIO container.
type MinIOServer struct {
	Container *minio.MinioContainer
	Endpoint  string // http://host:port
	User      string
	Password  string
}

var (
	minioOnce sync.Once
	minioSrv  *MinIOServer
	minioErr  error
)

// MinIO returns a MinIO server shared by the test process.
func MinIO(t testing.TB) *MinIOServer {
	t.Helper()
	minioOnce.Do(func() { minioSrv, minioErr = StartMinIO(context.Background()) })
	if minioErr != nil {
		t.Fatalf("start minio: %v", minioErr)
	}
	return minioSrv
}

// StartMinIO starts a new MinIO container. The caller terminates it.
func StartMinIO(ctx context.Context) (*MinIOServer, error) {
	// Docker Hub no longer serves minio/minio. quay.io is the other official registry of MinIO.
	c, err := minio.Run(ctx, "quay.io/minio/minio:latest", minio.WithUsername("sluiceminio"), minio.WithPassword("sluiceminio-secret"))
	if err != nil {
		return nil, err
	}
	addr, err := c.ConnectionString(ctx)
	if err != nil {
		return nil, err
	}
	return &MinIOServer{Container: c, Endpoint: "http://" + addr, User: "sluiceminio", Password: "sluiceminio-secret"}, nil
}

func (m *MinIOServer) client() *s3.Client {
	return s3.New(s3.Options{
		Region:       "us-east-1",
		BaseEndpoint: aws.String(m.Endpoint),
		UsePathStyle: true,
		Credentials:  credentials.NewStaticCredentialsProvider(m.User, m.Password, ""),
	})
}

// CreateBucket creates a bucket if it does not exist.
func (m *MinIOServer) CreateBucket(t testing.TB, bucket string) {
	t.Helper()
	_, err := m.client().CreateBucket(context.Background(), &s3.CreateBucketInput{Bucket: aws.String(bucket)})
	if err != nil {
		var exists bool
		_, herr := m.client().HeadBucket(context.Background(), &s3.HeadBucketInput{Bucket: aws.String(bucket)})
		exists = herr == nil
		if !exists {
			t.Fatalf("create bucket %s: %v", bucket, err)
		}
	}
}

// Config returns an s3 driver config with endpoint override and path-style addressing.
func (m *MinIOServer) Config(t testing.TB, bucket, prefix string) storage.S3Config {
	t.Helper()
	m.CreateBucket(t, bucket)
	return storage.S3Config{Bucket: bucket, Region: "us-east-1", Endpoint: m.Endpoint, ForcePathStyle: true,
		AccessKeyID: m.User, SecretAccessKey: m.Password, Prefix: prefix}
}

// RawKeys lists all keys of a bucket without a prefix.
func (m *MinIOServer) RawKeys(t testing.TB, bucket string) []string {
	t.Helper()
	out, err := m.client().ListObjectsV2(context.Background(), &s3.ListObjectsV2Input{Bucket: aws.String(bucket)})
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, o := range out.Contents {
		keys = append(keys, aws.ToString(o.Key))
	}
	return keys
}

// AzuriteServer is a running Azurite blob service.
type AzuriteServer struct {
	Container        testcontainers.Container
	ConnectionString string
	// AccountURL is the https blob endpoint in OAuth mode.
	AccountURL string
	// HTTPClient trusts the self-signed certificate of the OAuth mode.
	HTTPClient *http.Client
}

// Well-known Azurite development account.
const (
	azuriteAccount = "devstoreaccount1"
	azuriteKey     = "Eby8vdM02xNOcqFlqUwJPLlmEtlCDXJ1OUzFT50uSRZ6IFsuFq2UVErCz4I6tq/K1SZFPTOtr/KBHBeksoGMGw=="
)

// Azurite starts Azurite with plain HTTP and a shared key.
func Azurite(t testing.TB) *AzuriteServer {
	t.Helper()
	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "mcr.microsoft.com/azure-storage/azurite:latest",
			Cmd:          []string{"azurite-blob", "--blobHost", "0.0.0.0", "--skipApiVersionCheck", "--loose"},
			ExposedPorts: []string{"10000/tcp"},
			WaitingFor:   wait.ForListeningPort("10000/tcp").WithStartupTimeout(60 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start azurite: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })
	host, _ := c.Host(ctx)
	port, _ := c.MappedPort(ctx, "10000/tcp")
	endpoint := fmt.Sprintf("http://%s:%s/%s", host, port.Port(), azuriteAccount)
	return &AzuriteServer{Container: c, ConnectionString: fmt.Sprintf(
		"DefaultEndpointsProtocol=http;AccountName=%s;AccountKey=%s;BlobEndpoint=%s;", azuriteAccount, azuriteKey, endpoint)}
}

// AzuriteOAuth starts Azurite in OAuth mode. OAuth needs HTTPS, so the container
// uses a self-signed certificate that HTTPClient trusts.
func AzuriteOAuth(t testing.TB) *AzuriteServer {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	certPEM, keyPEM := selfSigned(t)
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image: "mcr.microsoft.com/azure-storage/azurite:latest",
			Cmd: []string{"azurite-blob", "--blobHost", "0.0.0.0", "--skipApiVersionCheck", "--loose",
				"--oauth", "basic", "--cert", "/certs/cert.pem", "--key", "/certs/key.pem"},
			ExposedPorts: []string{"10000/tcp"},
			Files: []testcontainers.ContainerFile{
				{HostFilePath: certPath, ContainerFilePath: "/certs/cert.pem", FileMode: 0o644},
				{HostFilePath: keyPath, ContainerFilePath: "/certs/key.pem", FileMode: 0o644},
			},
			WaitingFor: wait.ForListeningPort("10000/tcp").WithStartupTimeout(60 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start azurite oauth: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })
	host, _ := c.Host(ctx)
	port, _ := c.MappedPort(ctx, "10000/tcp")
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(certPEM)
	hc := &http.Client{Transport: &http.Transport{TLSClientConfig: tlsConfig(pool)}}
	if host == "localhost" {
		host = "127.0.0.1"
	}
	return &AzuriteServer{Container: c, AccountURL: fmt.Sprintf("https://%s:%s/%s", host, port.Port(), azuriteAccount), HTTPClient: hc}
}

func selfSigned(t testing.TB) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "azurite"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb})
}
