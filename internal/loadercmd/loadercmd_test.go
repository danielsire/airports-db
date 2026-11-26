package loadercmd

import (
	"errors"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		cmd     string
		args    []string
		want    Command
		wantErr bool
	}{
		{"load defaults to downloading", "load", nil, Command{Name: "load", Dir: "data"}, false},
		{"load local", "load", []string{"-local"}, Command{Name: "load", Dir: "data", Local: true}, false},
		{"load local with dir", "load", []string{"-local", "-dir", "/srv/csv"}, Command{Name: "load", Dir: "/srv/csv", Local: true}, false},
		{"load unknown flag", "load", []string{"-nope"}, Command{}, true},
		{"load stray argument", "load", []string{"extra"}, Command{}, true},
		{"status", "status", nil, Command{Name: "status", Dir: "data"}, false},
		{"status with argument", "status", []string{"x"}, Command{}, true},
		{"rollback", "rollback", nil, Command{Name: "rollback", Dir: "data"}, false},
		{"use with day", "use", []string{"20261107"}, Command{Name: "use", Dir: "data", Day: "20261107"}, false},
		{"use without day", "use", nil, Command{}, true},
		{"use with bad day", "use", []string{"2026-11-07"}, Command{}, true},
		{"use with two args", "use", []string{"20261107", "x"}, Command{}, true},
		{"unknown command", "drop", nil, Command{}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.cmd, tc.args)
			if tc.wantErr {
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("err = %v, want ErrInvalid", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	ok := []Command{
		{Name: "load"},
		{Name: "status"},
		{Name: "rollback"},
		{Name: "use", Day: "20261107"},
	}
	for _, c := range ok {
		if err := c.Validate(); err != nil {
			t.Errorf("%+v: %v", c, err)
		}
	}

	bad := []Command{
		{},             // empty name (e.g. missing ?cmd=)
		{Name: "LOAD"}, // case-sensitive
		{Name: "load; DROP TABLE airport"},
		{Name: "load", Day: "20261107"}, // day only belongs to "use"
		{Name: "use"},
		{Name: "use", Day: "20261340"}, // month 13
		{Name: "use", Day: "../../etc"},
	}
	for _, c := range bad {
		if err := c.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: err = %v, want ErrInvalid", c, err)
		}
	}
}

// Run must reject an invalid command before opening any database connection.
func TestRun_InvalidCommandNeverTouchesTheDatabase(t *testing.T) {
	t.Setenv("DB_HOST", "127.0.0.1")
	t.Setenv("DB_PORT", "1") // nothing listens here: a connection attempt would fail differently

	err := Run(nil, Command{Name: "use", Day: "bogus"}, nil) //nolint:staticcheck // ctx is never used on this path
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("err = %v, want ErrInvalid", err)
	}
}
