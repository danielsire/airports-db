package database

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"strconv"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/joho/godotenv/autoload"
)

type Service interface {
	DB() *sql.DB
	Health() map[string]string
	Close() error
}

type Config struct {
	Host     string
	Port     string
	Database string
	Username string
	Password string
	Schema   string // defaults to "public"
}

// ConfigFromEnv reads at call time (not at package init), so tests can use t.Setenv.
func ConfigFromEnv() Config {
	return Config{
		Host:     os.Getenv("DB_HOST"),
		Port:     os.Getenv("DB_PORT"),
		Database: os.Getenv("DB_DATABASE"),
		Username: os.Getenv("DB_USERNAME"),
		Password: os.Getenv("DB_PASSWORD"),
		Schema:   os.Getenv("DB_SCHEMA"),
	}
}

// DSN builds the connection string, escaping credentials.
func (c Config) DSN() string {
	schema := c.Schema
	if schema == "" {
		schema = "public"
	}
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(c.Username, c.Password),
		Host:     net.JoinHostPort(c.Host, c.Port),
		Path:     c.Database,
		RawQuery: url.Values{"sslmode": {"disable"}, "search_path": {schema}}.Encode(),
	}
	return u.String()
}

type service struct {
	db   *sql.DB
	name string
}

func New() (Service, error) {
	return NewFromConfig(ConfigFromEnv())
}

func NewFromConfig(cfg Config) (Service, error) {
	db, err := sql.Open("pgx", cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}

	return &service{db: db, name: cfg.Database}, nil
}

func (s *service) DB() *sql.DB { return s.db }

// Health(): unchanged

func (s *service) Close() error {
	log.Printf("Disconnected from database: %s", s.name)
	return s.db.Close()
}

func (s *service) Health() map[string]string {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	stats := make(map[string]string)

	if err := s.db.PingContext(ctx); err != nil {
		stats["status"] = "down"
		stats["error"] = fmt.Sprintf("db down: %v", err)
		return stats // no log.Fatalf: report, don't crash
	}

	stats["status"] = "up"
	stats["message"] = "It's healthy"

	dbStats := s.db.Stats()
	stats["open_connections"] = strconv.Itoa(dbStats.OpenConnections)
	stats["in_use"] = strconv.Itoa(dbStats.InUse)
	stats["idle"] = strconv.Itoa(dbStats.Idle)
	stats["wait_count"] = strconv.FormatInt(dbStats.WaitCount, 10)
	stats["wait_duration"] = dbStats.WaitDuration.String()
	stats["max_idle_closed"] = strconv.FormatInt(dbStats.MaxIdleClosed, 10)
	stats["max_lifetime_closed"] = strconv.FormatInt(dbStats.MaxLifetimeClosed, 10)

	return stats
}
