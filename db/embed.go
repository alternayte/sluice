// Package db embeds the SQL migrations.
package db

import "embed"

// Migrations holds the goose-format migration files.
//
//go:embed migrations/*.sql
var Migrations embed.FS
