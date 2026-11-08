//go:build integration

package loader_test

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"airports-db/internal/loader"
	"airports-db/internal/testutil"
)

var testDB *sql.DB

func TestMain(m *testing.M) {
	db, stop, err := testutil.StartPostgres(context.Background())
	if err != nil {
		panic(err)
	}
	testDB = db
	code := m.Run()
	stop() // os.Exit skips defers
	os.Exit(code)
}

// ---------------------------------------------------------------------------
// Fixtures and helpers
// ---------------------------------------------------------------------------

const airportsCSV = `"id","ident","type","name","latitude_deg","longitude_deg","elevation_ft","continent","iso_country","iso_region","municipality","scheduled_service","icao_code","iata_code","gps_code","local_code","home_link","wikipedia_link","keywords"
2434,"SBSJ","medium_airport","Sao Jose dos Campos",-23.2292,-45.8615,1873,"SA","BR","BR-SP","Sao Jose dos Campos","no","SBSJ","SJK","SBSJ",,,,
2435,"SP-H1","heliport","Paulista Heliport",-23.23,-45.86,,"SA","BR","BR-SP",,"no",,,,,,,
`

const runwaysCSV = `"id","airport_ref","airport_ident","length_ft","width_ft","surface","lighted","closed","le_ident","le_latitude_deg","le_longitude_deg","le_elevation_ft","le_heading_degT","le_displaced_threshold_ft","he_ident","he_latitude_deg","he_longitude_deg","he_elevation_ft","he_heading_degT","he_displaced_threshold_ft"
1,2434,"SBSJ",6562,148,"ASP",1,0,"15",-23.22,-45.87,,150,,"33",-23.24,-45.85,,330,
`

func day(n int) time.Time { return time.Date(2026, 11, n, 12, 0, 0, 0, time.UTC) }

// newLoader serves the given CSVs over HTTP and returns a loader pointed at them.
func newLoader(t *testing.T, airports, runways string) *loader.Loader {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/airports.csv":
			_, _ = io.WriteString(w, airports)
		case "/runways.csv":
			_, _ = io.WriteString(w, runways)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	a, r := loader.Airports, loader.Runways
	a.URL, a.MinRows = srv.URL+"/airports.csv", 1
	r.URL, r.MinRows = srv.URL+"/runways.csv", 1

	return &loader.Loader{
		DB:       testDB,
		Retain:   2,
		Datasets: []loader.Dataset{a, r},
		Now:      func() time.Time { return day(6) },
	}
}

// runOn runs a load as if today were November n, 2026.
func runOn(t *testing.T, l *loader.Loader, n int) {
	t.Helper()
	l.Now = func() time.Time { return day(n) }
	if err := l.Run(context.Background()); err != nil {
		t.Fatalf("load day %d: %v", n, err)
	}
}

func datedTables(t *testing.T, base string) []string {
	t.Helper()
	rows, err := testDB.Query(`SELECT tablename FROM pg_tables
		WHERE schemaname = current_schema() AND tablename ~ $1 ORDER BY tablename`,
		"^"+base+"_[0-9]{8}$")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

// wantTables asserts the exact dated tables that exist for both datasets (days as yyyymmdd).
func wantTables(t *testing.T, days ...string) {
	t.Helper()
	for _, base := range []string{"airport", "runway"} {
		var exp []string
		for _, d := range days {
			exp = append(exp, base+"_"+d)
		}
		if got := datedTables(t, base); !slices.Equal(got, exp) {
			t.Errorf("%s tables = %v, want %v", base, got, exp)
		}
	}
}

// cleanSlate drops every dated table and points the views back at the templates.
func cleanSlate(t *testing.T) {
	t.Helper()
	for _, base := range []string{"airport", "runway"} {
		for _, name := range datedTables(t, base) {
			if _, err := testDB.Exec(`DROP TABLE ` + name + ` CASCADE`); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := testDB.Exec(`CREATE OR REPLACE VIEW ` + base + ` AS SELECT * FROM ` + base + `_template`); err != nil {
			t.Fatal(err)
		}
	}
}

func count(t *testing.T, table string) int {
	t.Helper()
	var n int
	if err := testDB.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func publishedDay(t *testing.T, l *loader.Loader) string {
	t.Helper()
	st, err := l.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return st.Published
}

// ---------------------------------------------------------------------------
// Loading
// ---------------------------------------------------------------------------

func TestLoad_PublishesDatedTables(t *testing.T) {
	cleanSlate(t)
	l := newLoader(t, airportsCSV, runwaysCSV)

	if err := l.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	wantTables(t, "20261106")

	// the names the repositories use read the new data
	if n := count(t, "airport"); n != 2 {
		t.Errorf("airport rows = %d, want 2", n)
	}
	if n := count(t, "runway"); n != 1 {
		t.Errorf("runway rows = %d, want 1", n)
	}
	var def string
	if err := testDB.QueryRow(`SELECT pg_get_viewdef('airport'::regclass)`).Scan(&def); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(def, "airport_20261106") {
		t.Errorf("view does not point at today's table: %s", def)
	}

	// empty fields became NULL and created_at took its default
	var elevNull, muniNull bool
	err := testDB.QueryRow(`SELECT elevation_ft IS NULL, municipality IS NULL
		FROM airport WHERE ident = 'SP-H1'`).Scan(&elevNull, &muniNull)
	if err != nil || !elevNull || !muniNull {
		t.Errorf("NULL handling: elev=%v muni=%v err=%v", elevNull, muniNull, err)
	}
	var created time.Time
	if err := testDB.QueryRow(`SELECT created_at FROM airport WHERE ident = 'SBSJ'`).Scan(&created); err != nil || created.IsZero() {
		t.Errorf("created_at = %v err=%v", created, err)
	}

	// 1/0 became booleans, heading_degT landed in heading_deg
	var lighted, closed bool
	var heading float64
	err = testDB.QueryRow(`SELECT lighted, closed, le_heading_deg FROM runway WHERE id = 1`).Scan(&lighted, &closed, &heading)
	if err != nil {
		t.Fatal(err)
	}
	if !lighted || closed || heading != 150 {
		t.Errorf("lighted=%v closed=%v heading=%v, want true/false/150", lighted, closed, heading)
	}
}

func TestLoad_BadHeaderKeepsOldData(t *testing.T) {
	cleanSlate(t)
	good := newLoader(t, airportsCSV, runwaysCSV)
	if err := good.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	bad := newLoader(t, strings.Replace(airportsCSV, `"icao_code"`, `"gps_icao"`, 1), runwaysCSV)
	bad.Now = func() time.Time { return day(7) }
	if err := bad.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "unexpected CSV header") {
		t.Fatalf("err = %v, want a header error", err)
	}

	// nothing from the failed run is visible
	wantTables(t, "20261106")
	if n := count(t, "airport"); n != 2 {
		t.Errorf("airport rows = %d, want the old 2", n)
	}
}

func TestLoad_RefusesSmallFile(t *testing.T) {
	cleanSlate(t)
	l := newLoader(t, airportsCSV, runwaysCSV)
	l.Datasets[0].MinRows = 1000

	err := l.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "refusing to publish") {
		t.Fatalf("err = %v", err)
	}
	wantTables(t) // rolled back: no dated tables at all
	if n := count(t, "airport"); n != 0 {
		t.Errorf("view should still show the empty template, got %d rows", n)
	}
}

func TestLoad_HTTPError(t *testing.T) {
	cleanSlate(t)
	l := newLoader(t, airportsCSV, runwaysCSV)
	l.Datasets[0].URL += "/missing" // 404

	if err := l.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("err = %v, want a 404", err)
	}
	wantTables(t)
}

func TestLoad_SecondDatasetFailureRollsBackTheFirst(t *testing.T) {
	cleanSlate(t)
	l := newLoader(t, airportsCSV, "garbage\n")

	if err := l.Run(context.Background()); err == nil {
		t.Fatal("expected the runway load to fail")
	}
	wantTables(t) // the airport table must not survive either
}

// ---------------------------------------------------------------------------
// Retention: keep the published day and the last working day
// ---------------------------------------------------------------------------

// 08 ok, 09 ok -> both kept; 10 ok -> 09 and 10 only.
func TestRetention_KeepsPublishedAndPreviousDay(t *testing.T) {
	cleanSlate(t)
	l := newLoader(t, airportsCSV, runwaysCSV)

	runOn(t, l, 8)
	wantTables(t, "20261108")
	runOn(t, l, 9)
	wantTables(t, "20261108", "20261109")

	runOn(t, l, 10)
	wantTables(t, "20261109", "20261110")
	if got := publishedDay(t, l); got != "20261110" {
		t.Errorf("published = %s", got)
	}
}

// 07 ok, 08 fails -> 07 stays published and nothing is dropped; later 09 keeps 07.
func TestRetention_FailedLoadKeepsLastWorkingDay(t *testing.T) {
	cleanSlate(t)
	good := newLoader(t, airportsCSV, runwaysCSV)
	runOn(t, good, 7)

	bad := newLoader(t, strings.Replace(airportsCSV, `"icao_code"`, `"gps_icao"`, 1), runwaysCSV)
	bad.Now = func() time.Time { return day(8) }
	if err := bad.Run(context.Background()); err == nil {
		t.Fatal("expected the bad load to fail")
	}
	wantTables(t, "20261107")
	if got := publishedDay(t, good); got != "20261107" {
		t.Errorf("published = %s, want 20261107", got)
	}

	runOn(t, good, 9)
	wantTables(t, "20261107", "20261109")
}

// 08 turned out bad and was rolled back to 07: the next good load keeps 07 and drops 08.
func TestRetention_AfterRollbackKeepsTheWorkingDay(t *testing.T) {
	cleanSlate(t)
	l := newLoader(t, airportsCSV, runwaysCSV)
	runOn(t, l, 7)
	runOn(t, l, 8)

	if err := l.Use(context.Background(), "20261107"); err != nil {
		t.Fatal(err)
	}
	runOn(t, l, 9)

	wantTables(t, "20261107", "20261109")
}

func TestRetention_RerunSameDayKeepsPreviousDay(t *testing.T) {
	cleanSlate(t)
	l := newLoader(t, airportsCSV, runwaysCSV)
	runOn(t, l, 7)
	runOn(t, l, 8)
	runOn(t, l, 8)

	wantTables(t, "20261107", "20261108")
}

func TestRetention_Retain3(t *testing.T) {
	cleanSlate(t)
	l := newLoader(t, airportsCSV, runwaysCSV)
	l.Retain = 3

	for n := 7; n <= 10; n++ {
		runOn(t, l, n)
	}
	wantTables(t, "20261108", "20261109", "20261110")
}

// ---------------------------------------------------------------------------
// Switching: Use, Rollback, Status
// ---------------------------------------------------------------------------

func TestUse_SwitchesBetweenDays(t *testing.T) {
	cleanSlate(t)
	oneRow := strings.Join(strings.SplitAfter(airportsCSV, "\n")[:2], "") // header + first airport
	two := newLoader(t, airportsCSV, runwaysCSV)
	one := newLoader(t, oneRow, runwaysCSV)
	runOn(t, two, 7) // 2 airports
	runOn(t, one, 8) // 1 airport

	ctx := context.Background()
	if n := count(t, "airport"); n != 1 {
		t.Fatalf("airport rows = %d, want 1 (day 8)", n)
	}
	if err := two.Use(ctx, "20261107"); err != nil {
		t.Fatal(err)
	}
	if n := count(t, "airport"); n != 2 {
		t.Errorf("after use 20261107: %d rows, want 2", n)
	}
	if err := two.Use(ctx, "20261108"); err != nil { // forward again
		t.Fatal(err)
	}
	if n := count(t, "airport"); n != 1 {
		t.Errorf("after use 20261108: %d rows, want 1", n)
	}
}

func TestUse_RejectsUnknownAndIncompleteDays(t *testing.T) {
	cleanSlate(t)
	l := newLoader(t, airportsCSV, runwaysCSV)
	runOn(t, l, 7)
	ctx := context.Background()

	if err := l.Use(ctx, "20261231"); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Errorf("unknown day: err = %v", err)
	}

	// a day with only one of the two datasets must not be usable
	if _, err := testDB.Exec(`CREATE TABLE airport_20261201 (LIKE airport_template INCLUDING ALL)`); err != nil {
		t.Fatal(err)
	}
	if err := l.Use(ctx, "20261201"); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Errorf("incomplete day: err = %v", err)
	}

	if got := publishedDay(t, l); got != "20261107" {
		t.Errorf("published = %s, want 20261107 (unchanged)", got)
	}
}

func TestRollback(t *testing.T) {
	cleanSlate(t)
	l := newLoader(t, airportsCSV, runwaysCSV)
	ctx := context.Background()

	if _, err := l.Rollback(ctx); err == nil {
		t.Error("rollback with nothing published should fail")
	}

	runOn(t, l, 7)
	runOn(t, l, 8)

	got, err := l.Rollback(ctx)
	if err != nil || got != "20261107" {
		t.Fatalf("Rollback = %q, %v; want 20261107", got, err)
	}
	if p := publishedDay(t, l); p != "20261107" {
		t.Errorf("published = %s", p)
	}
	if _, err := l.Rollback(ctx); err == nil {
		t.Error("second rollback should fail: no older day")
	}
}

func TestStatus(t *testing.T) {
	cleanSlate(t)
	l := newLoader(t, airportsCSV, runwaysCSV)

	st, err := l.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Published != "" || len(st.Days) != 0 {
		t.Errorf("empty status = %+v", st)
	}

	runOn(t, l, 7)
	runOn(t, l, 8)

	st, err = l.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Published != "20261108" || !slices.Equal(st.Days, []string{"20261108", "20261107"}) {
		t.Errorf("status = %+v", st)
	}
}

// ---------------------------------------------------------------------------
// Offline mode: files from the data folder instead of a download
// ---------------------------------------------------------------------------

// offlineLoader writes the given CSVs into a temp folder and returns a loader
// that reads them. The URLs are unreachable, so any download attempt fails the test.
func offlineLoader(t *testing.T, airports, runways string) *loader.Loader {
	t.Helper()
	dir := t.TempDir()
	if airports != "" {
		if err := os.WriteFile(filepath.Join(dir, "airports.csv"), []byte(airports), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if runways != "" {
		if err := os.WriteFile(filepath.Join(dir, "runways.csv"), []byte(runways), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	a, r := loader.Airports, loader.Runways
	a.URL, a.MinRows = "http://127.0.0.1:1/never", 1
	r.URL, r.MinRows = "http://127.0.0.1:1/never", 1

	return &loader.Loader{
		DB:       testDB,
		Retain:   2,
		Offline:  true,
		DataDir:  dir,
		Datasets: []loader.Dataset{a, r},
		Now:      func() time.Time { return day(6) },
	}
}

func TestLoad_FromLocalFiles(t *testing.T) {
	cleanSlate(t)
	l := offlineLoader(t, airportsCSV, runwaysCSV)

	if err := l.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	wantTables(t, "20261106")
	if n := count(t, "airport"); n != 2 {
		t.Errorf("airport rows = %d, want 2", n)
	}
	if n := count(t, "runway"); n != 1 {
		t.Errorf("runway rows = %d, want 1", n)
	}
	if got := publishedDay(t, l); got != "20261106" {
		t.Errorf("published = %s", got)
	}
}

func TestLoad_LocalFileMissingRollsBack(t *testing.T) {
	cleanSlate(t)
	l := offlineLoader(t, airportsCSV, "") // runways.csv is absent

	err := l.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "open local file") {
		t.Fatalf("err = %v, want an open local file error", err)
	}
	wantTables(t) // the airport table from the same run must not survive
}

func TestLoad_LocalFileBadHeader(t *testing.T) {
	cleanSlate(t)
	l := offlineLoader(t, strings.Replace(airportsCSV, `"icao_code"`, `"gps_icao"`, 1), runwaysCSV)

	if err := l.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "unexpected CSV header") {
		t.Fatalf("err = %v, want a header error", err)
	}
	wantTables(t)
}

// A local load takes part in retention like any other load.
func TestLoad_LocalThenDownloadKeepsBothDays(t *testing.T) {
	cleanSlate(t)

	off := offlineLoader(t, airportsCSV, runwaysCSV)
	runOn(t, off, 7)

	online := newLoader(t, airportsCSV, runwaysCSV)
	runOn(t, online, 8)

	wantTables(t, "20261107", "20261108")
}
