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
			if len(migrations) != 2 || len(migrations[0].Statements) != len(tables())+len(indexes()) || len(migrations[1].Statements) != len(subjectExecutionStepTables())+len(subjectExecutionStepIndexes()) {
				t.Fatalf("unexpected migration inventory: %#v", migrations)
			}
			joined := strings.Join(migrations[0].Statements, "\n")
			for _, table := range tables() {
				if !strings.Contains(joined, table.name) {
					t.Fatalf("%s migration omitted %s", name, table.name)
				}
			}
			if !strings.Contains(strings.Join(migrations[1].Statements, "\n"), "_subject_steps") {
				t.Fatalf("%s migration omitted subject execution steps", name)
			}
			allMigrations := joined + "\n" + strings.Join(migrations[1].Statements, "\n")
			for _, retired := range []string{"_lifecycle_account_erasure_approvals", "_lifecycle_external_erasure_requests", "_lifecycle_deletion_registry", "_lifecycle_subject_erasure_fences", "_lifecycle_subject_execution_steps", "_lifecycle_archive_entries", "_lifecycle_file_artifacts"} {
				if strings.Contains(allMigrations, retired) {
					t.Fatalf("%s migration retained folded subject state table %s", name, retired)
				}
			}
			for _, column := range []string{"created_by", "requested_by", "owner_org_id"} {
				if !strings.Contains(joined, column) {
					t.Fatalf("%s foundation schema omitted %s", name, column)
				}
			}
			if strings.Contains(strings.ToLower(joined), "data_permissions") {
				t.Fatalf("%s lifecycle schema must not own data_permissions: %s", name, joined)
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
