package storage

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Config selects and configures the driver.
type Config struct {
	Type   string // postgres, fs, s3, azblob
	FSRoot string
	S3     S3Config
	Azblob AzblobConfig
}

// Open opens the configured driver. The postgres driver uses pool.
func Open(ctx context.Context, c Config, pool *pgxpool.Pool) (Store, error) {
	switch c.Type {
	case "", "postgres":
		return &Postgres{Pool: pool}, nil
	case "fs":
		return OpenFS(c.FSRoot)
	case "s3":
		return OpenS3(ctx, c.S3)
	case "azblob":
		return OpenAzblob(ctx, c.Azblob)
	default:
		return nil, fmt.Errorf("unknown storage type %q", c.Type)
	}
}
