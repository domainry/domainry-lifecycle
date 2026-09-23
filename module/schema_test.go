package module

import (
	"slices"
	"testing"

	"github.com/domainry/domainry-foundation/schemaownership"
	ormdialect "github.com/domainry/domainry-orm/dialect"
)

func TestPublicSchemaContractMatchesLifecycleInventory(t *testing.T) {
	tables := SchemaOwnership()
	if err := schemaownership.ValidateAll(tables); err != nil {
		t.Fatal(err)
	}
	if len(tables) != 2 || !slices.Equal(OwnedTables(), schemaownership.Names(tables)) {
		t.Fatalf("Lifecycle public schema inventory=%+v", tables)
	}
	if MigrationOwner != "lifecycle" {
		t.Fatalf("Lifecycle migration owner=%q", MigrationOwner)
	}
	dialect, err := ormdialect.New(ormdialect.SQLite)
	if err != nil {
		t.Fatal(err)
	}
	migrations, err := SchemaMigrations(dialect.WithSchema(""))
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) != 1 || migrations[0].Baseline != nil {
		t.Fatalf("Lifecycle public migrations=%+v", migrations)
	}

	tables[0].PrimaryKey[0] = "changed"
	if SchemaOwnership()[0].PrimaryKey[0] == "changed" {
		t.Fatal("Lifecycle public schema contract shares mutable primary-key storage")
	}
}
