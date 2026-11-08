package loader

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestTableName_ChangesWithTheDate(t *testing.T) {
	tests := []struct {
		day  time.Time
		want string
	}{
		{time.Date(2026, 11, 6, 23, 59, 0, 0, time.UTC), "airport_20261106"},
		{time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC), "airport_20261107"},
		{time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC), "airport_20260102"}, // zero-padded month/day
		{time.Date(2027, 12, 31, 12, 0, 0, 0, time.UTC), "airport_20271231"},
	}
	for _, tc := range tests {
		if got := TableName("airport", tc.day); got != tc.want {
			t.Errorf("TableName(%v) = %q, want %q", tc.day, got, tc.want)
		}
	}
}

func TestNew_UsesTheClockAndDefaults(t *testing.T) {
	l := New(nil)

	if got, want := TableName("airport", l.Now()), TableName("airport", time.Now()); got != want {
		t.Errorf("loader name = %q, want today's %q", got, want)
	}
	if l.retain() != 2 {
		t.Errorf("default retain = %d, want 2", l.retain())
	}
	if len(l.Datasets) != 2 {
		t.Errorf("datasets = %d, want airports and runways", len(l.Datasets))
	}
	if l.Offline {
		t.Error("downloading must be the default")
	}
	if l.dataDir() != "data" {
		t.Errorf("default data dir = %q, want data", l.dataDir())
	}
	if got := (&Loader{}).dataDir(); got != "data" {
		t.Errorf("zero-value data dir = %q, want data", got)
	}
}

func TestRetain_InvalidFallsBackToDefault(t *testing.T) {
	for _, n := range []int{0, -1} {
		if got := (&Loader{Retain: n}).retain(); got != 2 {
			t.Errorf("Retain=%d -> %d, want 2", n, got)
		}
	}
	if got := (&Loader{Retain: 3}).retain(); got != 3 {
		t.Errorf("Retain=3 -> %d", got)
	}
}

func TestChooseKeep(t *testing.T) {
	tests := []struct {
		name     string
		current  string
		before   string
		complete []string // newest first
		retain   int
		want     []string
	}{
		{"first ever load", "20261108", "", []string{"20261108"}, 2, []string{"20261108"}},
		{"second load keeps both", "20261109", "20261108", []string{"20261109", "20261108"}, 2, []string{"20261109", "20261108"}},
		{"third load drops the oldest", "20261110", "20261109", []string{"20261110", "20261109", "20261108"}, 2, []string{"20261110", "20261109"}},
		{"after a rollback keeps the working day, not the newest", "20261109", "20261107", []string{"20261109", "20261108", "20261107"}, 2, []string{"20261109", "20261107"}},
		{"previous day missing falls back to newest complete", "20261109", "20261108", []string{"20261109", "20261107"}, 2, []string{"20261109", "20261107"}},
		{"same-day rerun", "20261108", "20261108", []string{"20261108", "20261107"}, 2, []string{"20261108", "20261107"}},
		{"retain 3", "20261110", "20261109", []string{"20261110", "20261109", "20261108", "20261107"}, 3, []string{"20261110", "20261109", "20261108"}},
		{"retain 1 keeps only the current day", "20261110", "20261109", []string{"20261110", "20261109"}, 1, []string{"20261110"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := chooseKeep(tc.current, tc.before, tc.complete, tc.retain)
			if !slices.Equal(got, tc.want) {
				t.Errorf("chooseKeep = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCheckHeader(t *testing.T) {
	want := []string{"id", "ident", "type"}

	t.Run("matches", func(t *testing.T) {
		if err := checkHeader(`"id","ident","type"`+"\n", want); err != nil {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("tolerates BOM and CRLF", func(t *testing.T) {
		if err := checkHeader("\ufeff"+`"id","ident","type"`+"\r\n", want); err != nil {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("renamed column", func(t *testing.T) {
		err := checkHeader(`"id","ident","kind"`+"\n", want)
		if err == nil || !strings.Contains(err.Error(), "unexpected CSV header") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("reordered columns", func(t *testing.T) {
		if err := checkHeader(`"ident","id","type"`+"\n", want); err == nil {
			t.Error("expected an error: COPY maps by position")
		}
	})
	t.Run("extra column", func(t *testing.T) {
		if err := checkHeader(`"id","ident","type","extra"`+"\n", want); err == nil {
			t.Error("expected an error")
		}
	})
}

func TestCopySQL(t *testing.T) {
	got := copySQL("airport_20261106", []string{"id", "ident"})
	want := `COPY "airport_20261106" (id, ident) FROM STDIN WITH (FORMAT CSV, HEADER TRUE, DELIMITER ',', NULL '')`
	if got != want {
		t.Errorf("\n got: %s\nwant: %s", got, want)
	}
}

func TestIdentQuoting(t *testing.T) {
	if got := ident("airport_20261106"); got != `"airport_20261106"` {
		t.Errorf("ident = %s", got)
	}
	if got := ident(`a"b`); got != `"a""b"` {
		t.Errorf("embedded quote = %s", got)
	}
}

func TestDatasetDefinitions(t *testing.T) {
	for _, ds := range []Dataset{Airports, Runways} {
		t.Run(ds.Name, func(t *testing.T) {
			if len(ds.Header) != len(ds.Columns) {
				t.Errorf("header has %d entries, columns %d; COPY maps by position", len(ds.Header), len(ds.Columns))
			}
			if !strings.HasPrefix(ds.URL, "https://") {
				t.Errorf("URL = %q", ds.URL)
			}
			if ds.MinRows <= 0 {
				t.Error("MinRows must protect against truncated files")
			}
			if !strings.HasSuffix(ds.File, ".csv") {
				t.Errorf("File = %q, want a .csv name for offline loading", ds.File)
			}
		})
	}
	if len(Airports.Columns) != 19 {
		t.Errorf("airport columns = %d, want 19 (created_at takes its default)", len(Airports.Columns))
	}
	if len(Runways.Columns) != 20 {
		t.Errorf("runway columns = %d, want 20", len(Runways.Columns))
	}
}

// Invalid input is rejected before the database is touched.
func TestUse_InvalidDayFormat(t *testing.T) {
	l := New(nil)
	for _, in := range []string{"", "2026-11-07", "20261340", "abc", "2026110"} {
		err := l.Use(context.Background(), in)
		if err == nil || !strings.Contains(err.Error(), "invalid day") {
			t.Errorf("Use(%q) err = %v, want invalid day", in, err)
		}
	}
}

// ---------------------------------------------------------------------------
// Source selection: download vs local folder
// ---------------------------------------------------------------------------

func TestOpen_LocalFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "airports.csv"), []byte("id,ident\n1,SBSJ\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// the URL is unreachable on purpose: a local open must never touch the network
	l := &Loader{DataDir: dir}
	ds := Dataset{Name: "airport", File: "airports.csv", URL: "http://127.0.0.1:1/never"}

	src, source, err := l.open(context.Background(), ds, false)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()

	body, err := io.ReadAll(src)
	if err != nil || string(body) != "id,ident\n1,SBSJ\n" {
		t.Errorf("body = %q err = %v", body, err)
	}
	if want := filepath.Join(dir, "airports.csv"); source != want {
		t.Errorf("source = %q, want %q", source, want)
	}
}

func TestOpen_LocalFileMissing(t *testing.T) {
	l := &Loader{DataDir: t.TempDir()}

	_, _, err := l.open(context.Background(), Dataset{File: "airports.csv"}, false)
	if err == nil || !strings.Contains(err.Error(), "open local file") {
		t.Errorf("err = %v, want an open local file error", err)
	}
}

func TestOpen_DownloadUsesTheURLNotTheFolder(t *testing.T) {
	// an unreachable URL fails as a download error even though the local file exists
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "airports.csv"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l := &Loader{DataDir: dir}
	ds := Dataset{File: "airports.csv", URL: "http://127.0.0.1:1/never"}

	_, _, err := l.open(context.Background(), ds, true)
	if err == nil || !strings.Contains(err.Error(), "download") {
		t.Errorf("err = %v, want a download error", err)
	}
}
