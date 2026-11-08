package database

import (
	"airports-db/migrations"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func Migrate(db *sql.DB) error {
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	return goose.Up(db, ".")
}
