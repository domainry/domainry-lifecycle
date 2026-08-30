package schema

import (
	"strings"
	"testing"

	ormdialect "github.com/domainry/domainry-orm/dialect"
)

func TestPortableSchemaRendersAllSupportedDialects(t *testing.T) {
	t.Parallel()
	for _, name := range []ormdialect.Name{ormdialect.SQLite, ormdialect.Postgres, ormdialect.MySQL} {
		name := name
		t.Run(string(name), func(t *testing.T) {
			t.Parallel()
			dialect, err := ormdialect.New(name)
			if err != nil {
				t.Fatal(err)
			}
			migrations, err := Migrations(dialect.WithSchema("app"))
			if err != nil {
				t.Fatal(err)
			}
			if len(migrations) != 1 || len(migrations[0].Statements) != len(tables())+len(indexes()) {
				t.Fatalf("unexpected migration inventory: %#v", migrations)
			}
			joined := strings.Join(migrations[0].Statements, "\n")
			for _, table := range tables() {
				if !strings.Contains(joined, table.name) {
					t.Fatalf("%s migration omitted %s", name, table.name)
				}
			}
			if strings.Contains(strings.ToLower(joined), "if driver") {
				t.Fatalf("migration leaked driver branching: %s", joined)
			}
		})
	}
}

func TestMySQLSchemaPreservesPreExtractionPhysicalTypes(t *testing.T) {
	dialect, err := ormdialect.New(ormdialect.MySQL)
	if err != nil {
		t.Fatal(err)
	}
	values, err := Migrations(dialect.WithSchema("app"))
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.ToUpper(strings.Join(values[0].Statements, "\n"))
	if !strings.Contains(joined, "VARCHAR(191)") {
		t.Fatalf("MySQL identity columns must remain compatible with the pre-extraction schema: %s", joined)
	}
	if strings.Contains(joined, "VARCHAR(255)") || strings.Contains(joined, "LONGTEXT") {
		t.Fatalf("MySQL migration drifted from the pre-extraction schema: %s", joined)
	}
}
