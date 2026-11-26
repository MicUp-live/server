package main

import "testing"

func TestRequireLocalDatabaseURL(t *testing.T) {
	t.Parallel()

	for _, dsn := range []string{
		"postgres://micup:local@127.0.0.1:5432/micup?sslmode=disable",
		"postgres://micup:local@localhost:5432/micup?sslmode=disable",
		"postgresql://micup:local@[::1]:5432/micup?sslmode=disable",
		"postgres://micup:local@postgres:5432/micup?sslmode=disable",
	} {
		dsn := dsn
		t.Run(dsn, func(t *testing.T) {
			t.Parallel()
			if err := requireLocalDatabaseURL(dsn); err != nil {
				t.Fatalf("requireLocalDatabaseURL(%q): %v", dsn, err)
			}
		})
	}
}

func TestRequireLocalDatabaseURLRejectsRemoteAndEmptyDSNs(t *testing.T) {
	t.Parallel()

	for _, dsn := range []string{
		"",
		"postgres://micup:prod@prod-db.us-east-2.rds.amazonaws.com:5432/micup",
		"postgres://admin@abc.dsql.us-east-2.on.aws:5432/postgres",
		"https://127.0.0.1:5432/micup",
		"host=localhost dbname=micup",
	} {
		if err := requireLocalDatabaseURL(dsn); err == nil {
			t.Fatalf("requireLocalDatabaseURL(%q) succeeded", dsn)
		}
	}
}
