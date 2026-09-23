package schema

import (
	"slices"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/schemaownership"
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
			if migrations[0].Baseline != nil {
				t.Fatalf("%s Lifecycle migration still carries a legacy schema baseline", name)
			}
			joined := strings.Join(migrations[0].Statements, "\n")
			for _, table := range tables() {
				if !strings.Contains(joined, table.name) {
					t.Fatalf("%s migration omitted %s", name, table.name)
				}
			}
			if strings.Contains(joined, "_subject_requests") || strings.Contains(joined, "_subject_steps") {
				t.Fatalf("%s Lifecycle-owned migration retained shared Subject Lifecycle tables", name)
			}
			allMigrations := joined
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

func TestSchemaOwnershipMatchesCanonicalSQLiteDDL(t *testing.T) {
	dialect, err := ormdialect.New(ormdialect.SQLite)
	if err != nil {
		t.Fatal(err)
	}
	migrations, err := Migrations(dialect.WithSchema(""))
	if err != nil {
		t.Fatal(err)
	}
	ownership := SchemaOwnership()
	if err := schemaownership.ValidateAll(ownership); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(OwnedTables(), schemaownership.Names(ownership)) {
		t.Fatalf("Lifecycle owned tables=%v ownership=%+v", OwnedTables(), ownership)
	}
	for _, table := range ownership {
		var create string
		for _, statement := range migrations[0].Statements {
			if strings.Contains(statement, `CREATE TABLE IF NOT EXISTS "`+table.Name+`"`) {
				create = statement
				break
			}
		}
		if create == "" {
			t.Fatalf("Lifecycle table %s has ownership but no canonical DDL", table.Name)
		}
		quoted := make([]string, len(table.PrimaryKey))
		for index, column := range table.PrimaryKey {
			quoted[index] = `"` + column + `"`
		}
		if primaryKey := "PRIMARY KEY (" + strings.Join(quoted, ", ") + ")"; !strings.Contains(create, primaryKey) {
			t.Fatalf("Lifecycle table %s ownership primary key %v does not match DDL: %s", table.Name, table.PrimaryKey, create)
		}
	}
	joined := strings.Join(migrations[0].Statements, "\n")
	for _, retiredIndex := range []string{"uniq_lifecycle_hold_workspace_identity", "uniq_lifecycle_cleanup_workspace_identity"} {
		if strings.Contains(joined, retiredIndex) {
			t.Fatalf("Lifecycle final schema retained redundant identity index %s", retiredIndex)
		}
	}
}

func TestSchemaOwnershipReturnsIndependentValues(t *testing.T) {
	first, second := SchemaOwnership(), SchemaOwnership()
	first[0].PrimaryKey[0] = "changed"
	if second[0].PrimaryKey[0] == "changed" {
		t.Fatal("Lifecycle ownership primary keys share mutable storage")
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
