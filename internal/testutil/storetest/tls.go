package storetest

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
)

func tlsConfig(pool *x509.CertPool) *tls.Config {
	return &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
}

// ClientOptions returns client options. In OAuth mode the transport trusts the
// self-signed certificate of the container. Otherwise it returns nil (defaults).
func (a *AzuriteServer) ClientOptions() *container.ClientOptions {
	if a.HTTPClient == nil {
		return nil
	}
	return &container.ClientOptions{ClientOptions: policy.ClientOptions{Transport: a.HTTPClient}}
}

var _ policy.Transporter = (*http.Client)(nil)

// StaticTokenCredential returns a JWT that Azurite accepts in OAuth basic mode.
// Azurite checks issuer, audience and time, not the signature.
type StaticTokenCredential struct{}

// GetToken returns the token.
func (StaticTokenCredential) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	now := time.Now()
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	tok := enc(map[string]string{"alg": "HS256", "typ": "JWT"}) + "." + enc(map[string]any{
		"aud": "https://storage.azure.com",
		"iss": "https://sts.windows.net/ab1f708d-50f6-404c-a006-d71b2ac7a606/",
		"iat": now.Unix(), "nbf": now.Unix() - 60, "exp": now.Add(time.Hour).Unix(),
		"oid": "23657296-5cd5-45b0-a809-d972a7f4dfe1", "tid": "ab1f708d-50f6-404c-a006-d71b2ac7a606",
	}) + "." + base64.RawURLEncoding.EncodeToString([]byte("signature"))
	return azcore.AccessToken{Token: tok, ExpiresOn: now.Add(time.Hour)}, nil
}
