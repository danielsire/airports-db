//go:build integration

package database_test

import (
	"database/sql"
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/pressly/goose/v3"

	"airport-api/internal/database"
	"airport-api/migrations"
)

// freshDB creates an empty database inside the shared container and drops it afterwards.
// Uses testCfg and newService from database_integration_test.go.
func freshDB(t *testing.T) *sql.DB {
	t.Helper()

	admin := newService(t, testCfg)
	name := fmt.Sprintf("mig_%d", time.Now().UnixNano())

	if _, err := admin.DB().Exec("CREATE DATABASE " + name); err != nil {
		t.Fatalf("create database: %v", err)
	}

	cfg := testCfg
	cfg.Database = name
	svc := newService(t, cfg)

	// cleanups run LIFO: this drop runs before svc and admin are closed.
	// FORCE (Postgres 13+) kicks out any connection still open.
	t.Cleanup(func() {
		_, _ = admin.DB().Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)")
	})

	return svc.DB()
}

func exists(t *testing.T, db *sql.DB, query string, args ...any) bool {
	t.Helper()
	var ok bool
	if err := db.QueryRow(`SELECT EXISTS (`+query+`)`, args...).Scan(&ok); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return ok
}

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	return exists(t, db, `SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = $1`, name)
}

func functionExists(t *testing.T, db *sql.DB, name string) bool {
	return exists(t, db, `SELECT 1 FROM pg_proc WHERE proname = $1`, name)
}

func TestMigrate_UpCreatesSchema(t *testing.T) {
	db := freshDB(t)

	if err := database.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	for _, table := range []string{"airport", "runway"} {
		if !tableExists(t, db, table) {
			t.Errorf("table %s missing", table)
		}
	}

	t.Run("airport shape matches the repository's columns", func(t *testing.T) {
		var n int
		err := db.QueryRow(`SELECT count(*) FROM information_schema.columns WHERE table_name = 'airport'`).Scan(&n)
		if err != nil {
			t.Fatal(err)
		}
		// id..keywords (19) + created_at; must equal the 20 destinations in Airport.dests()
		if n != 20 {
			t.Errorf("airport has %d columns, want 20", n)
		}

		var typ string
		err = db.QueryRow(`SELECT data_type FROM information_schema.columns
			WHERE table_name = 'airport' AND column_name = 'created_at'`).Scan(&typ)
		if err != nil || typ != "timestamp with time zone" {
			t.Errorf("created_at type = %q (err %v)", typ, err)
		}
	})

	t.Run("indexes exist", func(t *testing.T) {
		for _, idx := range []string{
			"idx_airport_iata", "idx_airport_icao", "idx_airport_type",
			"idx_runway_airport_ident", "idx_runway_airport_ref",
		} {
			if !exists(t, db, `SELECT 1 FROM pg_indexes WHERE indexname = $1`, idx) {
				t.Errorf("index %s missing", idx)
			}
		}
	})

	t.Run("haversine_km", func(t *testing.T) {
		var km float64
		// one degree of latitude is ~111.19 km
		if err := db.QueryRow(`SELECT haversine_km(0, 0, 1, 0)`).Scan(&km); err != nil {
			t.Fatal(err)
		}
		if km < 111.0 || km > 111.4 {
			t.Errorf("distance = %f, want ~111.19", km)
		}

		// same point
		if err := db.QueryRow(`SELECT haversine_km(-23.2, -45.8, -23.2, -45.8)`).Scan(&km); err != nil || km != 0 {
			t.Errorf("same point: km=%f err=%v, want 0", km, err)
		}

		// antipodal points would push asin's argument above 1 through rounding;
		// the least(1.0, ...) guard must prevent a domain error
		if err := db.QueryRow(`SELECT haversine_km(0, 0, 0, 180)`).Scan(&km); err != nil {
			t.Fatalf("antipodal: %v", err)
		}
		if km < 20000 || km > 20030 {
			t.Errorf("antipodal = %f, want ~20015", km)
		}

		// STRICT: NULL in, NULL out (runways with a missing end rely on it)
		var n sql.NullFloat64
		if err := db.QueryRow(`SELECT haversine_km(0, 0, NULL, 0)`).Scan(&n); err != nil || n.Valid {
			t.Errorf("NULL input: %+v err=%v, want NULL", n, err)
		}
	})

	t.Run("constraints", func(t *testing.T) {
		_, err := db.Exec(`INSERT INTO airport (id, ident, type, name, latitude_deg, longitude_deg)
			VALUES (1, 'DUP', 'small_airport', 'a', 0, 0)`)
		if err != nil {
			t.Fatal(err)
		}
		_, err = db.Exec(`INSERT INTO airport (id, ident, type, name, latitude_deg, longitude_deg)
			VALUES (2, 'DUP', 'small_airport', 'b', 0, 0)`)
		if err == nil {
			t.Error("ident must be unique")
		}
		_, err = db.Exec(`INSERT INTO airport (id, ident, type, name, latitude_deg, longitude_deg)
			VALUES (3, 'NULLNAME', 'small_airport', NULL, 0, 0)`)
		if err == nil {
			t.Error("name must be NOT NULL")
		}
	})
}

func TestMigrate_Idempotent(t *testing.T) {
	db := freshDB(t)

	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := db.QueryRow(`SELECT count(*) FROM goose_db_version`).Scan(&before); err != nil {
		t.Fatal(err)
	}

	// running again must change nothing
	if err := database.Migrate(db); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	var after int
	if err := db.QueryRow(`SELECT count(*) FROM goose_db_version`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Errorf("version rows: %d before, %d after; second run applied something", before, after)
	}
}

func TestMigrate_VersionMatchesFiles(t *testing.T) {
	db := freshDB(t)
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}

	files, _ := fs.Glob(migrations.FS, "*.sql")
	re := regexp.MustCompile(`^(\d+)_`)
	max := 0
	for _, f := range files {
		if m := re.FindStringSubmatch(f); m != nil {
			if v, _ := strconv.Atoi(m[1]); v > max {
				max = v
			}
		}
	}

	got, err := goose.GetDBVersion(db)
	if err != nil {
		t.Fatal(err)
	}
	if int(got) != max {
		t.Errorf("db version = %d, want %d (highest migration file)", got, max)
	}
}

// Proves every Down works and that the whole chain can be re-applied.
func TestMigrate_DownThenUpAgain(t *testing.T) {
	db := freshDB(t)

	if err := database.Migrate(db); err != nil { // also sets goose's FS and dialect
		t.Fatal(err)
	}

	if err := goose.DownTo(db, ".", 0); err != nil {
		t.Fatalf("down to 0: %v", err)
	}
	for _, table := range []string{"airport", "runway"} {
		if tableExists(t, db, table) {
			t.Errorf("table %s still exists after down", table)
		}
	}
	if functionExists(t, db, "haversine_km") {
		t.Error("haversine_km still exists after down")
	}

	if err := database.Migrate(db); err != nil {
		t.Fatalf("up after down: %v", err)
	}
	if !tableExists(t, db, "airport") || !functionExists(t, db, "haversine_km") {
		t.Error("schema not restored after re-applying")
	}
}
