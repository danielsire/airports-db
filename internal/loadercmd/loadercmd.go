// Package loadercmd holds the loader commands (load, status, rollback, use) so
// that the CLI (cmd/loader) and the Lambda (cmd/lambda) run exactly the same code.
package loadercmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"airports-db/internal/database"
	"airports-db/internal/loader"
)

// ErrInvalid marks problems with the command itself (bad name, missing day...),
// as opposed to failures while running it. Lambda maps it to HTTP 400.
var ErrInvalid = errors.New("invalid command")

// Command is a fully described loader action.
type Command struct {
	Name  string // load | status | rollback | use
	Day   string // yyyymmdd, only for "use"
	Local bool   // load: read CSVs from Dir instead of downloading (CLI only)
	Dir   string // load: folder with airports.csv and runways.csv when Local
}

// Parse builds a Command from CLI-style arguments and validates it, before
// anything touches the database.
func Parse(name string, args []string) (Command, error) {
	c := Command{Name: name, Dir: "data"}

	switch name {
	case "load":
		fs := flag.NewFlagSet("load", flag.ContinueOnError)
		fs.BoolVar(&c.Local, "local", false, "read the CSV files from a folder instead of downloading them")
		fs.StringVar(&c.Dir, "dir", "data", "folder with airports.csv and runways.csv (used with -local)")
		if err := fs.Parse(args); err != nil {
			return c, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		if fs.NArg() > 0 {
			return c, fmt.Errorf("%w: unexpected arguments: %v", ErrInvalid, fs.Args())
		}
	case "use":
		if len(args) != 1 {
			return c, fmt.Errorf("%w: usage: loader use <yyyymmdd>", ErrInvalid)
		}
		c.Day = args[0]
	case "status", "rollback":
		if len(args) > 0 {
			return c, fmt.Errorf("%w: %s takes no arguments", ErrInvalid, name)
		}
	}
	return c, c.Validate()
}

// Validate checks the command without touching the database.
func (c Command) Validate() error {
	switch c.Name {
	case "load", "status", "rollback":
		if c.Day != "" {
			return fmt.Errorf("%w: %s does not take a day", ErrInvalid, c.Name)
		}
	case "use":
		if _, err := time.Parse("20060102", c.Day); err != nil {
			return fmt.Errorf("%w: use needs a day as yyyymmdd, got %q", ErrInvalid, c.Day)
		}
	default:
		return fmt.Errorf("%w: unknown command %q (load | status | rollback | use)", ErrInvalid, c.Name)
	}
	return nil
}

// Run opens the database (from the DB_* environment variables), executes the
// command and writes a human-readable result to out. The loader's own progress
// messages go through the standard logger.
func Run(ctx context.Context, c Command, out io.Writer) error {
	if err := c.Validate(); err != nil {
		return err
	}

	db, err := database.New()
	if err != nil {
		return err
	}
	defer db.Close()

	l := loader.New(db.DB())

	switch c.Name {
	case "load":
		l.Offline = c.Local
		if c.Dir != "" {
			l.DataDir = c.Dir
		}
		// makes sure the template tables, views and haversine_km exist
		if err := database.Migrate(db.DB()); err != nil {
			return err
		}
		if err := l.Run(ctx); err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, "load completed")
		return err

	case "status":
		st, err := l.Status(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "published: %s\n", orNone(st.Published))
		for _, d := range st.Days {
			mark := ""
			if d == st.Published {
				mark = "  <- published"
			}
			fmt.Fprintf(out, "  %s%s\n", d, mark)
		}
		return nil

	case "rollback":
		day, err := l.Rollback(ctx)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "views now point at %s\n", day)
		return err

	case "use":
		if err := l.Use(ctx, c.Day); err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "views now point at %s\n", c.Day)
		return err
	}
	return nil // unreachable: Validate rejects other names
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(none)"
	}
	return s
}
