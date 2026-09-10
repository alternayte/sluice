package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"gocloud.dev/blob"
	"gocloud.dev/blob/fileblob"
	"gocloud.dev/blob/s3blob"
	"gocloud.dev/gcerrors"
)

// Blob adapts a gocloud.dev bucket to Store (fs, s3, azblob).
type Blob struct {
	Bucket *blob.Bucket
	Name   string
	// beforeWrite, if set, goes to blob.WriterOptions.BeforeWrite on each Put.
	beforeWrite func(func(any) bool) error
}

// Driver returns the driver name.
func (b *Blob) Driver() string { return b.Name }

// Close closes the bucket.
func (b *Blob) Close() error { return b.Bucket.Close() }

func mapErr(err error) error {
	if err != nil && gcerrors.Code(err) == gcerrors.NotFound {
		return ErrNotFound
	}
	return err
}

// Put streams r into the bucket.
func (b *Blob) Put(ctx context.Context, key string, r io.Reader, contentType string) (int64, error) {
	if err := ValidKey(key); err != nil {
		return 0, err
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w, err := b.Bucket.NewWriter(ctx, key, &blob.WriterOptions{ContentType: contentType, BufferSize: 5 << 20, MaxConcurrency: 2, BeforeWrite: b.beforeWrite})
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(w, r)
	if err != nil {
		_ = w.Close()
		return n, err
	}
	return n, w.Close()
}

// Get opens a streaming reader.
func (b *Blob) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	r, err := b.Bucket.NewReader(ctx, key, nil)
	if err != nil {
		return nil, mapErr(err)
	}
	return r, nil
}

// Stat returns object attributes.
func (b *Blob) Stat(ctx context.Context, key string) (Info, error) {
	a, err := b.Bucket.Attributes(ctx, key)
	if err != nil {
		return Info{}, mapErr(err)
	}
	return Info{Key: key, Size: a.Size, ModTime: a.ModTime}, nil
}

// Delete removes the object. A missing key is not an error.
func (b *Blob) Delete(ctx context.Context, key string) error {
	err := b.Bucket.Delete(ctx, key)
	if err != nil && gcerrors.Code(err) == gcerrors.NotFound {
		return nil
	}
	return err
}

// List iterates over all objects with the prefix.
func (b *Blob) List(ctx context.Context, prefix string, fn func(Info) error) error {
	it := b.Bucket.List(&blob.ListOptions{Prefix: prefix})
	for {
		o, err := it.Next(ctx)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if o.IsDir {
			continue
		}
		if err := fn(Info{Key: o.Key, Size: o.Size, ModTime: o.ModTime}); err != nil {
			return err
		}
	}
}

// OpenFS opens the fs driver at root.
func OpenFS(root string) (*Blob, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return nil, err
	}
	bkt, err := fileblob.OpenBucket(abs, &fileblob.Options{CreateDir: true, NoTempDir: false})
	if err != nil {
		return nil, err
	}
	return &Blob{Bucket: bkt, Name: "fs"}, nil
}

// S3Config configures the s3 driver (REQ-STO-002).
type S3Config struct {
	Bucket          string
	Region          string
	Endpoint        string
	ForcePathStyle  bool
	AccessKeyID     string
	SecretAccessKey string
	Prefix          string
}

// OpenS3 opens the s3 driver. Empty static keys use the default AWS credential chain.
func OpenS3(ctx context.Context, c S3Config) (*Blob, error) {
	var opts []func(*awsconfig.LoadOptions) error
	if c.Region != "" {
		opts = append(opts, awsconfig.WithRegion(c.Region))
	} else {
		opts = append(opts, awsconfig.WithRegion("us-east-1"))
	}
	if c.AccessKeyID != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(c.AccessKeyID, c.SecretAccessKey, "")))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("s3 config: %w", err)
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if c.Endpoint != "" {
			o.BaseEndpoint = aws.String(c.Endpoint)
		}
		o.UsePathStyle = c.ForcePathStyle
		// R2 and MinIO do not need the newer checksum headers on every request.
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	})
	bkt, err := s3blob.OpenBucket(ctx, client, c.Bucket, nil)
	if err != nil {
		return nil, err
	}
	if c.Prefix != "" {
		bkt = blob.PrefixedBucket(bkt, normPrefix(c.Prefix))
	}
	return &Blob{Bucket: bkt, Name: "s3", beforeWrite: s3UploadLimits(client)}, nil
}

// S3 upload memory bounds (REQ-STO-004). The transfer manager keeps a pool of
// s3UploadConcurrency+1 part buffers of s3PartSize bytes. It reads the first
// part with io.ReadAll up to the multipart threshold, and io.ReadAll holds its
// chunks and the final copy together, so the first part costs up to twice the
// threshold. The default threshold is 16 MiB, which gives a live peak of
// about 47 MiB. With a threshold equal to the part size, the live peak of one
// upload is about 3*5 + 2*5 = 25 MiB, independent of the object size.
const (
	s3PartSize          = 5 << 20 // S3 minimum part size.
	s3UploadConcurrency = 2
)

// s3UploadLimits returns a BeforeWrite hook that replaces the transfer
// manager options of gocloud.dev/blob/s3blob. The s3blob writer keeps the
// *transfermanager.Client that it gives to the hook, so the hook sets the
// options in place. The WriterOptions BufferSize and MaxConcurrency cannot
// set the multipart threshold.
func s3UploadLimits(client *s3.Client) func(func(any) bool) error {
	return func(as func(any) bool) error {
		var tm *transfermanager.Client
		if !as(&tm) || tm == nil {
			return errors.New("s3 writer: no transfer manager client")
		}
		*tm = *transfermanager.New(client, func(o *transfermanager.Options) {
			o.PartSizeBytes = s3PartSize
			o.Concurrency = s3UploadConcurrency
			o.MultipartUploadThreshold = s3PartSize
		})
		return nil
	}
}

func normPrefix(p string) string {
	if p == "" || p[len(p)-1] == '/' {
		return p
	}
	return p + "/"
}
