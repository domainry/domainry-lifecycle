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
			if len(migrations) != 4 || len(migrations[0].Statements) != len(tables())+len(indexes()) || len(migrations[1].Statements) != len(subjectExecutionStepTables())+len(subjectExecutionStepIndexes()) || len(migrations[2].Statements) != 16 || len(migrations[3].Statements) != 2 {
				t.Fatalf("unexpected migration inventory: %#v", migrations)
			}
			joined := strings.Join(migrations[0].Statements, "\n")
			for _, table := range tables() {
				if !strings.Contains(joined, table.name) {
					t.Fatalf("%s migration omitted %s", name, table.name)
				}
			}
			if !strings.Contains(strings.Join(migrations[1].Statements, "\n"), "_lifecycle_subject_execution_steps") {
				t.Fatalf("%s migration omitted subject execution steps", name)
			}
			dataScope := strings.Join(migrations[2].Statements, "\n")
			for _, column := range []string{"published_by", "created_by", "requested_by", "owner_org_id"} {
				if !strings.Contains(dataScope, column) {
					t.Fatalf("%s data-scope migration omitted %s", name, column)
				}
			}
			for _, excluded := range []string{"data_permissions", "_lifecycle_subject_execution_steps", "_lifecycle_file_artifacts", "lease_owner", "fencing_token"} {
				if strings.Contains(strings.ToLower(dataScope), excluded) {
					t.Fatalf("%s data-scope migration touched excluded internal state %s: %s", name, excluded, dataScope)
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
