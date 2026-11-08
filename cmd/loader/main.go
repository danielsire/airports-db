package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"airports-db/internal/database"
	"airports-db/internal/loader"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	cmd, args := "load", os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}

	// parse and validate everything before touching the database
	var (
		local bool
		dir   string
	)
	switch cmd {
	//	loader                      same as "loader load" (what cron runs)
	//	loader load                 download today's data and publish it
	//	loader load -local          read data/airports.csv and data/runways.csv instead of downloading
	//	loader load -local -dir x   same, reading the files from folder x
	case "load":
		fs := flag.NewFlagSet("load", flag.ContinueOnError)
		fs.BoolVar(&local, "local", false, "read the CSV files from a folder instead of downloading them")
		fs.StringVar(&dir, "dir", "data", "folder with airports.csv and runways.csv (used with -local)")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if fs.NArg() > 0 {
			return fmt.Errorf("unexpected arguments: %v", fs.Args())
		}
		//	loader status               show the published day and the available days
		//	loader rollback             point the views at the newest day older than the published one
	case "status", "rollback":
		if len(args) > 0 {
			return fmt.Errorf("%s takes no arguments", cmd)
		}
		//	loader use <yyyymmdd>       point the views at a specific day
	case "use":
		if len(args) != 1 {
			return errors.New("usage: loader use <yyyymmdd>")
		}
	default:
		return fmt.Errorf("unknown command %q (load | status | rollback | use <yyyymmdd>)", cmd)
	}

	db, err := database.New()
	if err != nil {
		return err
	}
	defer db.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	l := loader.New(db.DB())

	switch cmd {
	case "load":
		l.Offline = local
		l.DataDir = dir

		// makes sure the template tables, views and haversine_km exist
		if err := database.Migrate(db.DB()); err != nil {
			return err
		}
		return l.Run(ctx)

	case "status":
		st, err := l.Status(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("published: %s\n", orNone(st.Published))
		for _, d := range st.Days {
			mark := ""
			if d == st.Published {
				mark = "  <- published"
			}
			fmt.Printf("  %s%s\n", d, mark)
		}
		return nil

	case "rollback":
		day, err := l.Rollback(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("views now point at %s\n", day)
		return nil

	case "use":
		return l.Use(ctx, args[0])
	}
	return nil
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(none)"
	}
	return s
}
