package database_test

import (
	"net/url"
	"strings"
	"testing"

	"airports-db/internal/database"
)

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("DB_HOST", "db.local")
	t.Setenv("DB_PORT", "5433")
	t.Setenv("DB_DATABASE", "airports")
	t.Setenv("DB_USERNAME", "app")
	t.Setenv("DB_PASSWORD", "secret")
	t.Setenv("DB_SCHEMA", "custom")

	want := database.Config{
		Host: "db.local", Port: "5433", Database: "airports",
		Username: "app", Password: "secret", Schema: "custom",
	}
	if got := database.ConfigFromEnv(); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestDSN(t *testing.T) {
	tests := []struct {
		name       string
		cfg        database.Config
		wantHost   string
		wantPort   string
		wantPass   string
		wantSchema string
	}{
		{
			name:       "defaults schema to public",
			cfg:        database.Config{Host: "localhost", Port: "5432", Database: "airports", Username: "app", Password: "app"},
			wantHost:   "localhost",
			wantPort:   "5432",
			wantPass:   "app",
			wantSchema: "public",
		},
		{
			name:       "special characters in password survive a round trip",
			cfg:        database.Config{Host: "localhost", Port: "5432", Database: "airports", Username: "app", Password: "p@ss/w:rd#1?x", Schema: "app"},
			wantHost:   "localhost",
			wantPort:   "5432",
			wantPass:   "p@ss/w:rd#1?x",
			wantSchema: "app",
		},
		{
			name:       "ipv6 host is bracketed",
			cfg:        database.Config{Host: "::1", Port: "5432", Database: "airports", Username: "app", Password: "app"},
			wantHost:   "::1",
			wantPort:   "5432",
			wantPass:   "app",
			wantSchema: "public",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			u, err := url.Parse(tc.cfg.DSN())
			if err != nil {
				t.Fatalf("DSN is not a valid URL: %v", err)
			}
			if u.Scheme != "postgres" {
				t.Errorf("scheme = %q", u.Scheme)
			}
			if u.Hostname() != tc.wantHost || u.Port() != tc.wantPort {
				t.Errorf("host:port = %s:%s, want %s:%s", u.Hostname(), u.Port(), tc.wantHost, tc.wantPort)
			}
			if u.Path != "/"+tc.cfg.Database {
				t.Errorf("path = %q", u.Path)
			}
			if pw, _ := u.User.Password(); pw != tc.wantPass {
				t.Errorf("password = %q, want %q", pw, tc.wantPass)
			}
			q := u.Query()
			if q.Get("search_path") != tc.wantSchema {
				t.Errorf("search_path = %q, want %q", q.Get("search_path"), tc.wantSchema)
			}
			if q.Get("sslmode") != "disable" {
				t.Errorf("sslmode = %q", q.Get("sslmode"))
			}
		})
	}
}

func TestNewFromConfig_Unreachable(t *testing.T) {
	// nothing listens on port 1, so this fails fast with "connection refused"
	svc, err := database.NewFromConfig(database.Config{
		Host: "127.0.0.1", Port: "1", Database: "x", Username: "x", Password: "x",
	})
	if err == nil {
		svc.Close()
		t.Fatal("expected an error")
	}
	if svc != nil {
		t.Error("service must be nil on error")
	}
	if !strings.Contains(err.Error(), "ping db") {
		t.Errorf("err = %v, want it to mention ping db", err)
	}
}
