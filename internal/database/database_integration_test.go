//go:build integration

package database_test

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"airport-api/internal/database"
)

var testCfg database.Config

func TestMain(m *testing.M) {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("test"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "start postgres:", err)
		os.Exit(1)
	}

	host, err := ctr.Host(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "host:", err)
		_ = testcontainers.TerminateContainer(ctr)
		os.Exit(1)
	}
	port, err := ctr.MappedPort(ctx, "5432/tcp")
	if err != nil {
		fmt.Fprintln(os.Stderr, "port:", err)
		_ = testcontainers.TerminateContainer(ctr)
		os.Exit(1)
	}

	testCfg = database.Config{
		Host: host, Port: port.Port(),
		Database: "test", Username: "test", Password: "test",
	}

	code := m.Run()
	_ = testcontainers.TerminateContainer(ctr) // os.Exit skips defers
	os.Exit(code)
}

func newService(t *testing.T, cfg database.Config) database.Service {
	t.Helper()
	svc, err := database.NewFromConfig(cfg)
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() }) // closing twice is harmless
	return svc
}

func TestNewFromConfig_Connects(t *testing.T) {
	svc := newService(t, testCfg)

	var one int
	if err := svc.DB().QueryRow("SELECT 1").Scan(&one); err != nil || one != 1 {
		t.Fatalf("SELECT 1: got %d, err %v", one, err)
	}
}

func TestNewFromConfig_WrongPassword(t *testing.T) {
	cfg := testCfg
	cfg.Password = "wrong"

	if _, err := database.NewFromConfig(cfg); err == nil {
		t.Fatal("expected an auth error")
	}
}

func TestSearchPathIsApplied(t *testing.T) {
	cfg := testCfg
	cfg.Schema = "app"
	svc := newService(t, cfg)

	var sp string
	if err := svc.DB().QueryRow("SHOW search_path").Scan(&sp); err != nil {
		t.Fatal(err)
	}
	if sp != "app" {
		t.Errorf("search_path = %q, want app", sp)
	}
}

func TestHealth_Up(t *testing.T) {
	svc := newService(t, testCfg)

	h := svc.Health()
	if h["status"] != "up" {
		t.Fatalf("status = %q, want up (%v)", h["status"], h)
	}
	for _, k := range []string{"open_connections", "in_use", "idle", "wait_count", "max_idle_closed", "max_lifetime_closed"} {
		v, ok := h[k]
		if !ok {
			t.Errorf("missing key %q", k)
			continue
		}
		if _, err := strconv.Atoi(v); err != nil {
			t.Errorf("%s = %q is not numeric", k, v)
		}
	}
	if h["wait_duration"] == "" {
		t.Error("missing wait_duration")
	}
}

// Regression test: the original Health() called log.Fatalf here,
// which would have killed the whole test binary.
func TestHealth_DownAfterClose(t *testing.T) {
	svc := newService(t, testCfg)
	if err := svc.Close(); err != nil {
		t.Fatal(err)
	}

	h := svc.Health()
	if h["status"] != "down" {
		t.Errorf("status = %q, want down", h["status"])
	}
	if h["error"] == "" {
		t.Error("expected an error message")
	}
}

func TestMigrate_AppliesSchemaAndIsIdempotent(t *testing.T) {
	svc := newService(t, testCfg)

	for i := 1; i <= 2; i++ { // second run must be a no-op
		if err := database.Migrate(svc.DB()); err != nil {
			t.Fatalf("migrate run %d: %v", i, err)
		}
	}

	for _, table := range []string{"airport", "runway"} {
		var exists bool
		err := svc.DB().QueryRow(
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = $1)`, table,
		).Scan(&exists)
		if err != nil || !exists {
			t.Errorf("table %s missing (err %v)", table, err)
		}
	}

	var km float64
	if err := svc.DB().QueryRow(`SELECT haversine_km(0, 0, 1, 0)`).Scan(&km); err != nil {
		t.Fatalf("haversine_km: %v", err)
	}
	if km < 111.0 || km > 111.4 {
		t.Errorf("haversine_km = %f, want ~111.19", km)
	}
}
