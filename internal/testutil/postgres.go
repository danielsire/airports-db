package testutil

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"airports-db/internal/database"
)

// StartPostgres launches a throwaway Postgres, runs the real migrations
// (so haversine_km and the schema are tested too) and returns a pool.
// Call the returned func to stop the container.
func StartPostgres(ctx context.Context) (*sql.DB, func(), error) {
	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("test"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("start container: %w", err)
	}
	cleanup := func() { _ = testcontainers.TerminateContainer(ctr) }

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	if err := database.Migrate(db); err != nil {
		db.Close()
		cleanup()
		return nil, nil, fmt.Errorf("migrate: %w", err)
	}

	return db, func() { db.Close(); cleanup() }, nil
}

// Reset empties the given tables between tests.
func Reset(t testing.TB, db *sql.DB, tables ...string) {
	t.Helper()
	for _, tbl := range tables {
		if _, err := db.Exec("DELETE FROM " + tbl); err != nil {
			t.Fatalf("delete from %s: %v", tbl, err)
		}
	}
}
