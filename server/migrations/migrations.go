// Package migrations embeds the SQL schema files so the migrate command ships
// as a single binary — no "did you copy the .sql files to the server" step.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
