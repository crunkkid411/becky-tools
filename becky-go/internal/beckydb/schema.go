package beckydb

import _ "embed"

// Schema is the canonical becky forensic schema, embedded from schema.sql so the
// runnable DDL and the documented file never drift. EnsureSchema runs it.
//
//go:embed schema.sql
var Schema string

// SchemaFTS is the FTS5 keyword-index DDL, embedded from schema_fts.sql. It is
// kept separate from Schema so EnsureSchema can apply it in its own sqlite3
// invocation and treat an FTS5-unavailable failure as non-fatal (a sqlite3 built
// without FTS5 errors on the CREATE; running it apart from the core tables means
// that error can't abort the rest of the schema).
//
//go:embed schema_fts.sql
var SchemaFTS string
