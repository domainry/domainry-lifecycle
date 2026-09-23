// Package schema contains Lifecycle-owned, portable schema declarations.
// Dialect differences are rendered exclusively by domainry-orm.
package schema

import (
	"fmt"

	ormschema "github.com/domainry/domainry-orm/schema"

	"github.com/domainry/domainry-lifecycle-sdk/modulehost"
	ormmigration "github.com/domainry/domainry-orm/migration"
)

type table struct {
	name    string
	columns []ormschema.ColumnDefinition
	primary []string
}

type index struct {
	table, name string
	unique      bool
	columns     []string
}

func Migrations(renderer modulehost.Dialect) ([]modulehost.SchemaMigration, error) {
	if renderer == nil {
		return nil, fmt.Errorf("lifecycle migrations require host dialect")
	}
	foundation, err := buildMigration(renderer, 1, "foundation", tables(), indexes())
	if err != nil {
		return nil, err
	}
	return []modulehost.SchemaMigration{foundation}, nil
}

func buildMigration(renderer modulehost.Dialect, version uint, name string, tableDefinitions []table, indexDefinitions []index) (modulehost.SchemaMigration, error) {
	statements := make([]string, 0, len(tableDefinitions)+len(indexDefinitions))
	baseline := ormmigration.Baseline{Tables: make([]ormmigration.Table, 0, len(tableDefinitions))}
	for _, definition := range tableDefinitions {
		builder := ormschema.NewTable(renderer, definition.name).
			IfNotExists().Columns(definition.columns...)
		if len(definition.primary) > 0 {
			builder.PrimaryKey(definition.primary...)
		}
		query, args, err := builder.Build()
		if err != nil {
			return modulehost.SchemaMigration{}, fmt.Errorf("build lifecycle table %s: %w", definition.name, err)
		}
		if len(args) != 0 {
			return modulehost.SchemaMigration{}, fmt.Errorf("lifecycle table %s produced DDL arguments", definition.name)
		}
		statements = append(statements, query)
		physical, err := builder.PhysicalTable()
		if err != nil {
			return modulehost.SchemaMigration{}, fmt.Errorf("build lifecycle baseline %s: %w", definition.name, err)
		}
		baselineTable := ormmigration.Table{Name: physical.Name, Columns: make([]ormmigration.Column, len(physical.Columns))}
		for index, column := range physical.Columns {
			baselineTable.Columns[index] = ormmigration.Column{Name: column.Name, Type: column.Type, Nullable: column.Nullable, PrimaryKey: column.PrimaryKey}
		}
		baseline.Tables = append(baseline.Tables, baselineTable)
	}
	for _, definition := range indexDefinitions {
		// The host ledger guarantees one execution. MySQL has no portable
		// CREATE INDEX IF NOT EXISTS form, so idempotency belongs to the
		// migration registrar rather than a driver branch in this module.
		builder := ormschema.NewIndex(renderer, definition.name, definition.table).
			Columns(definition.columns...)
		if definition.unique {
			builder.Unique()
		}
		query, args, err := builder.Build()
		if err != nil {
			return modulehost.SchemaMigration{}, fmt.Errorf("build lifecycle index %s: %w", definition.name, err)
		}
		if len(args) != 0 {
			return modulehost.SchemaMigration{}, fmt.Errorf("lifecycle index %s produced DDL arguments", definition.name)
		}
		statements = append(statements, query)
		for tableIndex := range baseline.Tables {
			if baseline.Tables[tableIndex].Name == definition.table {
				baseline.Tables[tableIndex].Indexes = append(baseline.Tables[tableIndex].Indexes, ormmigration.Index{Name: definition.name, Unique: definition.unique, Columns: append([]string(nil), definition.columns...)})
				break
			}
		}
	}
	return modulehost.SchemaMigration{Version: version, Name: name, Statements: statements, Baseline: &baseline}, nil
}

func key(name string) ormschema.ColumnDefinition {
	return ormschema.Column(name, ormschema.TextKey(191)).NotNull()
}
func optionalKey(name string) ormschema.ColumnDefinition {
	return ormschema.Column(name, ormschema.TextKey(191)).NotNull().DefaultValue("")
}
func text(name string) ormschema.ColumnDefinition {
	return ormschema.Column(name, ormschema.Text()).NotNull()
}
func bigint(name string) ormschema.ColumnDefinition {
	return ormschema.Column(name, ormschema.BigInt()).NotNull()
}
func tables() []table {
	return []table{
		{"_lifecycle_legal_holds", []ormschema.ColumnDefinition{key("id"), key("workspace_id"), optionalKey("owner"), optionalKey("resource_type"), optionalKey("resource_id"), optionalKey("created_by"), optionalKey("owner_org_id"), key("starts_at"), optionalKey("ends_at"), key("review_at"), text("payload_json")}, nil},
		{"_lifecycle_cleanup_jobs", []ormschema.ColumnDefinition{key("id"), key("workspace_id"), optionalKey("operation_id"), optionalKey("requested_by"), optionalKey("owner_org_id"), key("policy_key"), key("policy_version"), key("status"), optionalKey("checkpoint_value"), optionalKey("lease_owner"), optionalKey("lease_expires_at"), ormschema.Column("fencing_token", ormschema.BigInt()).NotNull().DefaultValue(int64(0)), key("updated_at"), text("payload_json")}, nil},
	}
}

func indexes() []index {
	return []index{
		{"_lifecycle_legal_holds", "uniq_lifecycle_hold_workspace_identity", true, []string{"workspace_id", "id"}},
		{"_lifecycle_legal_holds", "idx_lifecycle_hold_scope", false, []string{"workspace_id", "owner", "resource_type", "resource_id"}},
		{"_lifecycle_legal_holds", "idx_lifecycle_hold_creator", false, []string{"workspace_id", "created_by"}},
		{"_lifecycle_legal_holds", "idx_lifecycle_hold_owner_org", false, []string{"workspace_id", "owner_org_id"}},
		{"_lifecycle_cleanup_jobs", "uniq_lifecycle_cleanup_workspace_identity", true, []string{"workspace_id", "id"}},
		{"_lifecycle_cleanup_jobs", "idx_lifecycle_cleanup_claim", false, []string{"workspace_id", "status", "lease_expires_at", "updated_at"}},
		{"_lifecycle_cleanup_jobs", "idx_lifecycle_cleanup_operation", false, []string{"workspace_id", "operation_id"}},
		{"_lifecycle_cleanup_jobs", "idx_lifecycle_cleanup_requester", false, []string{"workspace_id", "requested_by"}},
		{"_lifecycle_cleanup_jobs", "idx_lifecycle_cleanup_owner_org", false, []string{"workspace_id", "owner_org_id"}},
	}
}
