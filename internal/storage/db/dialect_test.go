package db

import "testing"

func TestRebind(t *testing.T) {
	d := postgresDialect{}
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "two placeholders", in: "SELECT * FROM t WHERE a = ? AND b = ?", want: "SELECT * FROM t WHERE a = $1 AND b = $2"},
		{name: "placeholder in string", in: "INSERT INTO t (v) VALUES ('?')", want: "INSERT INTO t (v) VALUES ('?')"},
		{name: "placeholder in quoted identifier", in: `SELECT "?" FROM t WHERE id = ?`, want: `SELECT "?" FROM t WHERE id = $1`},
		{name: "line comment", in: "SELECT ? -- comment ?\nWHERE x = ?", want: "SELECT $1 -- comment ?\nWHERE x = $2"},
		{name: "block comment", in: "SELECT ? /* ? */ WHERE y = ?", want: "SELECT $1 /* ? */ WHERE y = $2"},
		{name: "dollar quoted", in: "SELECT $$?$$, ?", want: "SELECT $$?$$, $1"},
		{name: "tagged dollar quoted", in: "SELECT $tag$?$tag$, ?", want: "SELECT $tag$?$tag$, $1"},
		{name: "escaped quote", in: "SELECT 'it''s ?' AS s, ?", want: "SELECT 'it''s ?' AS s, $1"},
		{name: "no placeholder", in: "SELECT 1", want: "SELECT 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := d.Rebind(tt.in); got != tt.want {
				t.Fatalf("Rebind(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSQLiteDialectIsPassthrough(t *testing.T) {
	d := sqliteDialect{}
	q := "SELECT * FROM t WHERE a = ? AND b = ?"
	if got := d.Rebind(q); got != q {
		t.Fatalf("sqlite Rebind(%q) = %q, want unchanged", q, got)
	}
}

func TestDialectFor(t *testing.T) {
	for _, name := range []string{"sqlite", "postgres"} {
		d, err := DialectFor(name)
		if err != nil || d.Name() != name {
			t.Fatalf("DialectFor(%q) = %v, %v", name, d, err)
		}
	}
	if _, err := DialectFor("mysql"); err == nil {
		t.Fatal("DialectFor(mysql) error = nil, want error")
	}
}
