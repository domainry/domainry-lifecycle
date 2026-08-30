// Package migrations contains Lifecycle-owned, portable schema declarations.
// Dialect differences are rendered exclusively by domainry-orm.
package migrations

import (
	"context"
	"fmt"

	"github.com/domainry/domainry-lifecycle/modulehost"
	ormbuilder "github.com/domainry/domainry-orm/builder"
	ormmigration "github.com/domainry/domainry-orm/migration"
)

const Owner = "lifecycle"

type table struct {
	name    string
	columns []ormbuilder.SchemaColumn
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
	tableDefinitions, indexDefinitions := tables(), indexes()
	statements := make([]string, 0, len(tableDefinitions)+len(indexDefinitions))
	baseline := ormmigration.Baseline{Tables: make([]ormmigration.Table, 0, len(tableDefinitions))}
	for _, definition := range tableDefinitions {
		builder := ormbuilder.NewCreateTableBuilder(renderer, definition.name).
			IfNotExists().WithoutSystemColumns().Columns(definition.columns...)
		if len(definition.primary) > 0 {
			builder.PrimaryKey(definition.primary...)
		}
		query, args, err := builder.Build()
		if err != nil {
			return nil, fmt.Errorf("build lifecycle table %s: %w", definition.name, err)
		}
		if len(args) != 0 {
			return nil, fmt.Errorf("lifecycle table %s produced DDL arguments", definition.name)
		}
		statements = append(statements, query)
		physical, err := builder.PhysicalTable()
		if err != nil {
			return nil, fmt.Errorf("build lifecycle baseline %s: %w", definition.name, err)
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
		builder := ormbuilder.NewCreateIndexBuilder(renderer, definition.name, definition.table).
			Columns(definition.columns...)
		if definition.unique {
			builder.Unique()
		}
		query, args, err := builder.Build()
		if err != nil {
			return nil, fmt.Errorf("build lifecycle index %s: %w", definition.name, err)
		}
		if len(args) != 0 {
			return nil, fmt.Errorf("lifecycle index %s produced DDL arguments", definition.name)
		}
		statements = append(statements, query)
		for tableIndex := range baseline.Tables {
			if baseline.Tables[tableIndex].Name == definition.table {
				baseline.Tables[tableIndex].Indexes = append(baseline.Tables[tableIndex].Indexes, ormmigration.Index{Name: definition.name, Unique: definition.unique, Columns: append([]string(nil), definition.columns...)})
				break
			}
		}
	}
	return []modulehost.SchemaMigration{{Version: 1, Name: "foundation", Statements: statements, Baseline: &baseline}}, nil
}

func Apply(ctx context.Context, host modulehost.Host) error {
	if host == nil || host.Migrations() == nil {
		return fmt.Errorf("lifecycle migrations require host registrar")
	}
	values, err := Migrations(host.Dialect())
	if err != nil {
		return err
	}
	return host.Migrations().ApplyOwnedMigrations(ctx, Owner, values)
}

func key(name string) ormbuilder.SchemaColumn {
	return ormbuilder.DefineColumn(name, ormbuilder.TextKeyType(191)).NotNull()
}
func optionalKey(name string) ormbuilder.SchemaColumn {
	return ormbuilder.DefineColumn(name, ormbuilder.TextKeyType(191)).NotNull().DefaultValue("")
}
func text(name string) ormbuilder.SchemaColumn {
	return ormbuilder.DefineColumn(name, ormbuilder.TextType()).NotNull()
}
func bigint(name string) ormbuilder.SchemaColumn {
	return ormbuilder.DefineColumn(name, ormbuilder.BigIntType()).NotNull()
}
func boolean(name string, value bool) ormbuilder.SchemaColumn {
	return ormbuilder.DefineColumn(name, ormbuilder.BooleanType()).NotNull().DefaultValue(value)
}

func tables() []table {
	return []table{
		{"lifecycle_policy_versions", []ormbuilder.SchemaColumn{key("workspace_id"), key("policy_key"), key("version"), bigint("revision"), key("status"), text("payload_json"), key("published_at")}, nil},
		{"lifecycle_legal_holds", []ormbuilder.SchemaColumn{key("id"), key("workspace_id"), optionalKey("owner"), optionalKey("resource_type"), optionalKey("resource_id"), key("starts_at"), optionalKey("ends_at"), key("review_at"), text("payload_json")}, nil},
		{"lifecycle_cleanup_jobs", []ormbuilder.SchemaColumn{key("id"), key("workspace_id"), key("policy_key"), key("policy_version"), key("status"), optionalKey("checkpoint_value"), optionalKey("lease_owner"), optionalKey("lease_expires_at"), ormbuilder.DefineColumn("fencing_token", ormbuilder.BigIntType()).NotNull().DefaultValue(int64(0)), key("updated_at"), text("payload_json")}, nil},
		{"lifecycle_subject_requests", []ormbuilder.SchemaColumn{key("id"), key("workspace_id"), key("kind"), key("status"), key("subject_id"), optionalKey("resolved_identity"), optionalKey("download_expires_at"), key("updated_at"), text("payload_json")}, nil},
		{"lifecycle_external_erasures", []ormbuilder.SchemaColumn{key("id"), key("request_id"), key("workspace_id"), key("status"), text("payload_json")}, nil},
		{"lifecycle_audit_evidence", []ormbuilder.SchemaColumn{key("id"), key("workspace_id"), key("event"), key("resource_id"), optionalKey("policy_key"), key("created_at"), text("payload_json")}, nil},
		{"lifecycle_archive_entries", []ormbuilder.SchemaColumn{key("id"), key("workspace_id"), key("owner"), key("source_table"), key("resource_id"), key("policy_key"), key("policy_version"), key("job_id"), key("payload_hash"), text("payload_json"), key("archived_at")}, nil},
		{"lifecycle_deletion_registry", []ormbuilder.SchemaColumn{key("request_id"), key("workspace_id"), key("resolved_identity"), boolean("backup_pending", true), text("evidence"), key("updated_at")}, nil},
		{"lifecycle_file_artifacts", []ormbuilder.SchemaColumn{key("id"), key("workspace_id"), key("object_key"), key("field_key"), key("filename"), key("content_type"), key("sha256"), bigint("size_bytes"), key("status"), ormbuilder.DefineColumn("scan_status", ormbuilder.TextKeyType(191)).NotNull().DefaultValue("pending"), optionalKey("scan_provider"), optionalKey("scan_evidence_ref"), optionalKey("scanned_at"), key("created_at"), optionalKey("last_referenced_at"), optionalKey("delete_after"), optionalKey("deleted_at")}, nil},
	}
}

func indexes() []index {
	return []index{
		{"lifecycle_policy_versions", "uniq_lifecycle_policy_version", true, []string{"workspace_id", "policy_key", "version"}},
		{"lifecycle_policy_versions", "uniq_lifecycle_policy_revision", true, []string{"workspace_id", "policy_key", "revision"}},
		{"lifecycle_legal_holds", "uniq_lifecycle_hold_workspace_identity", true, []string{"workspace_id", "id"}},
		{"lifecycle_legal_holds", "idx_lifecycle_hold_scope", false, []string{"workspace_id", "owner", "resource_type", "resource_id"}},
		{"lifecycle_cleanup_jobs", "uniq_lifecycle_cleanup_workspace_identity", true, []string{"workspace_id", "id"}},
		{"lifecycle_cleanup_jobs", "idx_lifecycle_cleanup_claim", false, []string{"workspace_id", "status", "lease_expires_at", "updated_at"}},
		{"lifecycle_subject_requests", "uniq_lifecycle_subject_workspace_identity", true, []string{"workspace_id", "id"}},
		{"lifecycle_subject_requests", "idx_lifecycle_subject_identity", false, []string{"workspace_id", "subject_id", "status", "updated_at"}},
		{"lifecycle_external_erasures", "uniq_lifecycle_external_workspace_identity", true, []string{"workspace_id", "id"}},
		{"lifecycle_external_erasures", "idx_lifecycle_external_request", false, []string{"workspace_id", "request_id", "status"}},
		{"lifecycle_audit_evidence", "uniq_lifecycle_audit_workspace_identity", true, []string{"workspace_id", "id"}},
		{"lifecycle_audit_evidence", "idx_lifecycle_audit_workspace", false, []string{"workspace_id", "created_at"}},
		{"lifecycle_archive_entries", "uniq_lifecycle_archive_workspace_identity", true, []string{"workspace_id", "id"}},
		{"lifecycle_archive_entries", "idx_lifecycle_archive_source", false, []string{"workspace_id", "source_table", "resource_id", "archived_at"}},
		{"lifecycle_deletion_registry", "uniq_lifecycle_deletion_workspace_identity", true, []string{"workspace_id", "request_id"}},
		{"lifecycle_file_artifacts", "uniq_lifecycle_file_workspace_name", true, []string{"workspace_id", "filename"}},
		{"lifecycle_file_artifacts", "uniq_lifecycle_file_workspace_identity", true, []string{"workspace_id", "id"}},
		{"lifecycle_file_artifacts", "idx_lifecycle_file_cleanup", false, []string{"status", "delete_after", "created_at"}},
	}
}
