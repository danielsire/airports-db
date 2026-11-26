To run

go run ./cmd/loader                  # same as "load" (what cron runs)
go run ./cmd/loader status
go run ./cmd/loader rollback         # newest working day older than the published one
go run ./cmd/loader use 20261107     # a specific day (also moves forward again)

go run ./cmd/loader                    # download (default, what cron runs)
go run ./cmd/loader load -local        # read data/airports.csv and data/runways.csv
go run ./cmd/loader load -local -dir /srv/ourairports
