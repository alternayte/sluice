package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"gocloud.dev/blob"
	"gocloud.dev/blob/azureblob"
)

// AzblobConfig configures the azblob driver (REQ-STO-003).
type AzblobConfig struct {
	// AccountURL uses the credential from CredentialFactory (DefaultAzureCredential).
	AccountURL string
	// ConnectionString is used instead of AccountURL when set.
	ConnectionString string
	Container        string
	Prefix           string
	// ClientOptions are optional client options (transport, retries).
	ClientOptions *container.ClientOptions
	// CredentialFactory creates the token credential for AccountURL.
	// Nil uses DefaultCredentialFactory.
	CredentialFactory CredentialFactory
}

// CredentialFactory creates an Azure token credential.
type CredentialFactory func() (azcore.TokenCredential, error)

// DefaultCredentialFactory selects DefaultAzureCredential: environment, workload
// identity, managed identity and developer credentials.
func DefaultCredentialFactory() (azcore.TokenCredential, error) {
	return azidentity.NewDefaultAzureCredential(nil)
}

// OpenAzblob opens the azblob driver. It creates the container when it is missing
// and the credential allows it.
func OpenAzblob(ctx context.Context, c AzblobConfig) (*Blob, error) {
	if c.Container == "" {
		return nil, errors.New("azblob: container is required")
	}
	var client *container.Client
	var err error
	switch {
	case c.ConnectionString != "":
		client, err = container.NewClientFromConnectionString(c.ConnectionString, c.Container, c.ClientOptions)
	case c.AccountURL != "":
		factory := c.CredentialFactory
		if factory == nil {
			factory = DefaultCredentialFactory
		}
		var cred azcore.TokenCredential
		cred, err = factory()
		if err != nil {
			return nil, fmt.Errorf("azblob credential: %w", err)
		}
		client, err = container.NewClient(strings.TrimRight(c.AccountURL, "/")+"/"+c.Container, cred, c.ClientOptions)
	default:
		return nil, errors.New("azblob: account URL or connection string is required")
	}
	if err != nil {
		return nil, fmt.Errorf("azblob client: %w", err)
	}
	// A credential without container create permission can still use an existing container.
	if _, err := client.Create(ctx, nil); err != nil && !bloberror.HasCode(err, bloberror.ContainerAlreadyExists, bloberror.AuthorizationPermissionMismatch) {
		return nil, fmt.Errorf("azblob create container: %w", err)
	}
	bkt, err := azureblob.OpenBucket(ctx, client, nil)
	if err != nil {
		return nil, err
	}
	if c.Prefix != "" {
		bkt = blob.PrefixedBucket(bkt, normPrefix(c.Prefix))
	}
	return &Blob{Bucket: bkt, Name: "azblob"}, nil
}
