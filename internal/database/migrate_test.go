package database_test

import (
	"io/fs"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"airports-db/migrations"
)

var migrationName = regexp.MustCompile(`^(\d{5})_[a-z0-9_]+\.sql$`)

func TestMigrationFiles(t *testing.T) {
	files, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no .sql files embedded; check the //go:embed directive")
	}

	var versions []int
	for _, f := range files {
		m := migrationName.FindStringSubmatch(f)
		if m == nil {
			t.Errorf("%s: name must look like 00001_description.sql", f)
			continue
		}
		v, _ := strconv.Atoi(m[1])
		versions = append(versions, v)

		body, err := fs.ReadFile(migrations.FS, f)
		if err != nil {
			t.Fatal(err)
		}
		sql := string(body)
		if !strings.Contains(sql, "-- +goose Up") {
			t.Errorf("%s: missing '-- +goose Up'", f)
		}
		if !strings.Contains(sql, "-- +goose Down") {
			t.Errorf("%s: missing '-- +goose Down' (every migration should be reversible)", f)
		}
		// functions contain semicolons and need the statement markers
		if strings.Contains(strings.ToUpper(sql), "CREATE OR REPLACE FUNCTION") &&
			!strings.Contains(sql, "-- +goose StatementBegin") {
			t.Errorf("%s: function body needs StatementBegin/StatementEnd", f)
		}
	}

	// versions must be unique, start at 1 and have no gaps
	slices.Sort(versions)
	for i, v := range versions {
		if v != i+1 {
			t.Errorf("versions = %v; expected 1..%d with no gaps or duplicates", versions, len(versions))
			break
		}
	}
}
