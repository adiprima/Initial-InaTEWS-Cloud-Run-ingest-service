package migrations

import "embed"

// Files contains ordered SQL migrations used by the migration command.
//
//go:embed *.sql
var Files embed.FS
