package postgres

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// EnsureSchema applies embedded SQL migrations idempotently, executing one DDL
// statement at a time for Amazon Aurora DSQL compatibility.
func (s *Store) EnsureSchema(ctx context.Context) error {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		raw, err := migrationsFS.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		for _, stmt := range splitSQLStatements(string(raw)) {
			if s.dsqlMode {
				stmt = rewriteForDSQL(stmt)
			}
			if _, err := s.pool.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("exec migration %s (%s): %w", entry.Name(), truncateSQL(stmt), err)
			}
		}
	}
	return nil
}

func splitSQLStatements(sqlText string) []string {
	parts := strings.Split(sqlText, ";")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		lines := strings.Split(part, "\n")
		cleaned := make([]string, 0, len(lines))
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "--") {
				continue
			}
			cleaned = append(cleaned, line)
		}
		stmt := strings.TrimSpace(strings.Join(cleaned, "\n"))
		if stmt != "" {
			out = append(out, stmt)
		}
	}
	return out
}

func rewriteForDSQL(stmt string) string {
	upper := strings.ToUpper(stmt)
	if strings.HasPrefix(upper, "CREATE UNIQUE INDEX ") && !strings.HasPrefix(upper, "CREATE UNIQUE INDEX ASYNC ") {
		return "CREATE UNIQUE INDEX ASYNC " + stmt[len("CREATE UNIQUE INDEX "):]
	}
	if strings.HasPrefix(upper, "CREATE INDEX ") && !strings.HasPrefix(upper, "CREATE INDEX ASYNC ") {
		return "CREATE INDEX ASYNC " + stmt[len("CREATE INDEX "):]
	}
	return stmt
}

func truncateSQL(stmt string) string {
	first, _, _ := strings.Cut(stmt, "\n")
	if len(first) > 80 {
		return first[:80] + "..."
	}
	return first
}
