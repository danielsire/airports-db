package loader

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

const (
	dateLayout = "20060102" // Go's layout for yyyyMMdd (reference time is 2006-01-02)
	lockKey    = 7245001    // advisory lock: only one load/switch runs at a time
)

// ---------------------------------------------------------------------------
// Datasets
// ---------------------------------------------------------------------------

// Dataset describes one CSV and where it goes.
type Dataset struct {
	Name    string   // logical name; also the view name ("airport")
	URL     string   // CSV download URL (used when downloading)
	File    string   // file name inside Loader.DataDir (used when not downloading)
	Header  []string // exact CSV header we expect (COPY maps by position, not by name)
	Columns []string // COPY target columns, in CSV order
	MinRows int64    // refuse to publish a suspiciously small file
}

var airportColumns = []string{
	"id", "ident", "type", "name", "latitude_deg", "longitude_deg", "elevation_ft",
	"continent", "iso_country", "iso_region", "municipality", "scheduled_service",
	"icao_code", "iata_code", "gps_code", "local_code", "home_link", "wikipedia_link", "keywords",
}

var Airports = Dataset{
	Name:    "airport",
	URL:     "https://davidmegginson.github.io/ourairports-data/airports.csv",
	File:    "airports.csv",
	Header:  airportColumns,
	Columns: airportColumns,
	MinRows: 30_000,
}

var Runways = Dataset{
	Name: "runway",
	URL:  "https://davidmegginson.github.io/ourairports-data/runways.csv",
	File: "runways.csv",
	// the CSV calls them *_heading_degT; our columns drop the "T"
	Header: []string{
		"id", "airport_ref", "airport_ident", "length_ft", "width_ft", "surface", "lighted", "closed",
		"le_ident", "le_latitude_deg", "le_longitude_deg", "le_elevation_ft", "le_heading_degT", "le_displaced_threshold_ft",
		"he_ident", "he_latitude_deg", "he_longitude_deg", "he_elevation_ft", "he_heading_degT", "he_displaced_threshold_ft",
	},
	Columns: []string{
		"id", "airport_ref", "airport_ident", "length_ft", "width_ft", "surface", "lighted", "closed",
		"le_ident", "le_latitude_deg", "le_longitude_deg", "le_elevation_ft", "le_heading_degt", "le_displaced_threshold_ft",
		"he_ident", "he_latitude_deg", "he_longitude_deg", "he_elevation_ft", "he_heading_degt", "he_displaced_threshold_ft",
	},
	MinRows: 20_000,
}

// ---------------------------------------------------------------------------
// Loader
// ---------------------------------------------------------------------------

const defaultDataDir = "data"

type Loader struct {
	DB       *sql.DB
	Client   *http.Client     // nil = http.DefaultClient
	Now      func() time.Time // injectable for tests
	Retain   int              // dated days kept per dataset, including the published one (default 2)
	Offline  bool             // false (default): download from Dataset.URL; true: read Dataset.File from DataDir
	DataDir  string           // folder with the CSV files when Offline (default "data")
	Datasets []Dataset
}

func New(db *sql.DB) *Loader {
	return &Loader{
		DB:       db,
		Now:      time.Now,
		Retain:   2, // the published day + the last working day
		DataDir:  defaultDataDir,
		Datasets: []Dataset{Airports, Runways},
	}
}

func (l *Loader) dataDir() string {
	if l.DataDir == "" {
		return defaultDataDir
	}
	return l.DataDir
}

func (l *Loader) retain() int {
	if l.Retain < 1 {
		return 2
	}
	return l.Retain
}

// TableName returns e.g. airport_20261106.
func TableName(base string, day time.Time) string {
	return tableFor(base, day.Format(dateLayout))
}

func tableFor(base, day string) string { return base + "_" + day }

// ---------------------------------------------------------------------------
// Load
// ---------------------------------------------------------------------------

// Run downloads every dataset into today's tables, publishes them and prunes
// old days. Nothing becomes visible, and nothing is dropped, unless the whole
// run succeeds.
func (l *Loader) Run(ctx context.Context) error {
	day := l.Now().Format(dateLayout)

	return l.inTx(ctx, func(conn *pgx.Conn, tx pgx.Tx) error {
		// what the views serve right now = the last known-working day
		before, err := l.publishedDay(ctx, tx)
		if err != nil {
			return err
		}

		for _, ds := range l.Datasets {
			if err := l.loadDataset(ctx, conn, tx, ds, tableFor(ds.Name, day), !l.Offline); err != nil {
				return fmt.Errorf("load %s: %w", ds.Name, err)
			}
		}
		if err := l.publish(ctx, tx, day); err != nil {
			return err
		}

		complete, err := l.completeDays(ctx, tx)
		if err != nil {
			return err
		}
		keep := chooseKeep(day, before, complete, l.retain())

		for _, ds := range l.Datasets {
			if err := l.prune(ctx, tx, ds, keep); err != nil {
				return fmt.Errorf("prune %s: %w", ds.Name, err)
			}
		}
		return nil
	})
}

// chooseKeep decides which days survive a successful load: the day just
// published, then the day the views served before it (the last working day),
// then the newest remaining complete days, up to retain in total. Only days
// that exist for every dataset are eligible.
func chooseKeep(current, before string, complete []string, retain int) []string {
	candidates := append([]string{current, before}, complete...)

	var keep []string
	for _, d := range candidates {
		if d == "" || slices.Contains(keep, d) || !slices.Contains(complete, d) {
			continue
		}
		keep = append(keep, d)
		if len(keep) == retain {
			break
		}
	}
	return keep
}

// open returns the CSV source and a description of where it came from.
// download=true fetches ds.URL; download=false reads ds.File from DataDir.
func (l *Loader) open(ctx context.Context, ds Dataset, download bool) (io.ReadCloser, string, error) {
	if !download {
		path := filepath.Join(l.dataDir(), ds.File)
		f, err := os.Open(path)
		if err != nil {
			return nil, "", fmt.Errorf("open local file: %w", err)
		}
		return f, path, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ds.URL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "airport-api-loader")

	client := l.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("download: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, "", fmt.Errorf("download: %s", resp.Status)
	}
	return resp.Body, ds.URL, nil
}

// loadDataset copies one CSV into table. When download is true the CSV comes
// from ds.URL, otherwise from ds.File inside the loader's data folder.
func (l *Loader) loadDataset(ctx context.Context, conn *pgx.Conn, tx pgx.Tx, ds Dataset, table string, download bool) error {
	src, source, err := l.open(ctx, ds, download)
	if err != nil {
		return err
	}
	defer src.Close()

	// COPY maps columns by position, so verify the header before trusting the file.
	br := bufio.NewReader(src)
	headerLine, err := br.ReadString('\n')
	if err != nil {
		return fmt.Errorf("read header: %w", err)
	}
	if err := checkHeader(headerLine, ds.Header); err != nil {
		return err
	}

	tbl := ident(table)
	// Re-running on the same day replaces that day's table (views are repointed at publish).
	if _, err := tx.Exec(ctx, "DROP TABLE IF EXISTS "+tbl+" CASCADE"); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "CREATE TABLE "+tbl+" (LIKE "+ident(ds.Name+"_template")+" INCLUDING ALL)"); err != nil {
		return err
	}

	// Put the header back so the COPY statement can keep HEADER TRUE.
	body := io.MultiReader(strings.NewReader(headerLine), br)
	tag, err := conn.PgConn().CopyFrom(ctx, body, copySQL(table, ds.Columns))
	if err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	if tag.RowsAffected() < ds.MinRows {
		return fmt.Errorf("only %d rows (expected at least %d); refusing to publish", tag.RowsAffected(), ds.MinRows)
	}

	if _, err := tx.Exec(ctx, "ANALYZE "+tbl); err != nil { // fresh table, no planner stats yet
		return err
	}
	log.Printf("loaded %d rows into %s (from %s)", tag.RowsAffected(), table, source)
	return nil
}

// prune drops the dated tables of ds whose day is not in keep.
func (l *Loader) prune(ctx context.Context, tx pgx.Tx, ds Dataset, keep []string) error {
	days, err := datasetDays(ctx, tx, ds.Name)
	if err != nil {
		return err
	}
	for _, d := range days {
		if slices.Contains(keep, d) {
			continue
		}
		name := tableFor(ds.Name, d)
		// no CASCADE: if something unexpected depends on it, fail instead of destroying it
		if _, err := tx.Exec(ctx, "DROP TABLE "+ident(name)); err != nil {
			return err
		}
		log.Printf("dropped old table %s", name)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Switching the views
// ---------------------------------------------------------------------------

// Use points every view at the tables of an existing day (yyyymmdd).
// The day must exist for every dataset, so airports and runways never mix days.
func (l *Loader) Use(ctx context.Context, day string) error {
	if _, err := time.Parse(dateLayout, day); err != nil {
		return fmt.Errorf("invalid day %q (want yyyymmdd)", day)
	}
	return l.inTx(ctx, func(_ *pgx.Conn, tx pgx.Tx) error {
		days, err := l.completeDays(ctx, tx)
		if err != nil {
			return err
		}
		if !slices.Contains(days, day) {
			return fmt.Errorf("day %s is not available for every dataset (available: %s)",
				day, strings.Join(days, ", "))
		}
		return l.publish(ctx, tx, day)
	})
}

// Rollback points the views at the newest complete day older than the
// published one, and returns that day.
func (l *Loader) Rollback(ctx context.Context) (string, error) {
	var target string
	err := l.inTx(ctx, func(_ *pgx.Conn, tx pgx.Tx) error {
		current, err := l.publishedDay(ctx, tx)
		if err != nil {
			return err
		}
		if current == "" {
			return errors.New("no day is published; nothing to roll back from")
		}
		days, err := l.completeDays(ctx, tx) // newest first
		if err != nil {
			return err
		}
		for _, d := range days {
			if d < current { // yyyymmdd compares correctly as a string
				target = d
				break
			}
		}
		if target == "" {
			return fmt.Errorf("no working day older than %s to roll back to", current)
		}
		return l.publish(ctx, tx, target)
	})
	return target, err
}

// Status describes what is published and what is available.
type Status struct {
	Published string   // day the views read from; "" while they still show the empty template
	Days      []string // days available for every dataset, newest first
}

func (l *Loader) Status(ctx context.Context) (Status, error) {
	var st Status
	err := l.withConn(ctx, func(conn *pgx.Conn) error {
		var err error
		if st.Published, err = l.publishedDay(ctx, conn); err != nil {
			return err
		}
		st.Days, err = l.completeDays(ctx, conn)
		return err
	})
	return st, err
}

// publish repoints every view to the given day, inside the caller's transaction.
func (l *Loader) publish(ctx context.Context, tx pgx.Tx, day string) error {
	for _, ds := range l.Datasets {
		table := tableFor(ds.Name, day)
		q := "CREATE OR REPLACE VIEW " + ident(ds.Name) + " AS SELECT * FROM " + ident(table)
		if _, err := tx.Exec(ctx, q); err != nil {
			return fmt.Errorf("publish %s: %w", ds.Name, err)
		}
		log.Printf("%s now reads from %s", ds.Name, table)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Introspection
// ---------------------------------------------------------------------------

// querier is satisfied by both *pgx.Conn and pgx.Tx.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func queryStrings(ctx context.Context, q querier, sql string, args ...any) ([]string, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// datasetDays lists the yyyymmdd suffixes of base's dated tables, newest first.
func datasetDays(ctx context.Context, q querier, base string) ([]string, error) {
	return queryStrings(ctx, q, `
		SELECT substring(tablename FROM '[0-9]{8}$')
		FROM pg_tables
		WHERE schemaname = current_schema() AND tablename ~ $1
		ORDER BY 1 DESC`, "^"+base+"_[0-9]{8}$")
}

// completeDays are the days present for every dataset, newest first.
func (l *Loader) completeDays(ctx context.Context, q querier) ([]string, error) {
	var result []string
	for i, ds := range l.Datasets {
		days, err := datasetDays(ctx, q, ds.Name)
		if err != nil {
			return nil, err
		}
		if i == 0 {
			result = days
			continue
		}
		result = slices.DeleteFunc(result, func(d string) bool { return !slices.Contains(days, d) })
	}
	return result, nil
}

// publishedDay is the day the first dataset's view reads from ("" if it still
// shows the template). All views are always switched together.
func (l *Loader) publishedDay(ctx context.Context, q querier) (string, error) {
	days, err := queryStrings(ctx, q,
		`SELECT COALESCE(substring(pg_get_viewdef(to_regclass($1)::oid) FROM '[0-9]{8}'), '')`,
		l.Datasets[0].Name)
	if err != nil || len(days) == 0 {
		return "", err
	}
	return days[0], nil
}

// ---------------------------------------------------------------------------
// Connection plumbing and helpers
// ---------------------------------------------------------------------------

// withConn runs fn on a native pgx connection borrowed from database/sql.
func (l *Loader) withConn(ctx context.Context, fn func(*pgx.Conn) error) error {
	c, err := l.DB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("get connection: %w", err)
	}
	defer c.Close()

	return c.Raw(func(driverConn any) error {
		sc, ok := driverConn.(*stdlib.Conn)
		if !ok {
			return fmt.Errorf("unexpected driver connection %T (the loader needs pgx)", driverConn)
		}
		return fn(sc.Conn())
	})
}

// inTx runs fn in one transaction, serialized by an advisory lock so a load and
// a switch can never interleave. It commits only if fn succeeds.
func (l *Loader) inTx(ctx context.Context, fn func(conn *pgx.Conn, tx pgx.Tx) error) error {
	return l.withConn(ctx, func(conn *pgx.Conn) error {
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit

		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", lockKey); err != nil {
			return err
		}
		if err := fn(conn, tx); err != nil {
			return err
		}
		return tx.Commit(ctx)
	})
}

func checkHeader(line string, want []string) error {
	line = strings.TrimPrefix(line, "\ufeff") // tolerate a BOM
	got, err := csv.NewReader(strings.NewReader(line)).Read()
	if err != nil {
		return fmt.Errorf("parse header: %w", err)
	}
	if !slices.Equal(got, want) {
		return fmt.Errorf("unexpected CSV header (did the file format change?)\n got:  %s\nwant: %s",
			strings.Join(got, ","), strings.Join(want, ","))
	}
	return nil
}

// Table and column names are constants plus a formatted date, never user input,
// and table names are quoted anyway.
func ident(name string) string { return pgx.Identifier{name}.Sanitize() }

func copySQL(table string, columns []string) string {
	return fmt.Sprintf(
		"COPY %s (%s) FROM STDIN WITH (FORMAT CSV, HEADER TRUE, DELIMITER ',', NULL '')",
		ident(table), strings.Join(columns, ", "))
}
