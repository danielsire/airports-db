// Command loader refreshes the airport and runway tables from OurAirports.
//
//	loader                      same as "loader load" (what cron runs)
//	loader load                 download today's data and publish it
//	loader load -local          read data/airports.csv and data/runways.csv instead of downloading
//	loader load -local -dir x   same, reading the files from folder x
//	loader status               show the published day and the available days
//	loader rollback             point the views at the newest day older than the published one
//	loader use <yyyymmdd>       point the views at a specific day
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"airports-db/internal/loadercmd"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	name, args := "load", os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name, args = args[0], args[1:]
	}

	// parse and validate before touching the database
	cmd, err := loadercmd.Parse(name, args)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	return loadercmd.Run(ctx, cmd, os.Stdout)
}
