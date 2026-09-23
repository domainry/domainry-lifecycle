package schema

import "github.com/domainry/domainry-foundation/schemaownership"

const (
	MigrationOwner            = "lifecycle"
	LifecycleCleanupJobsTable = "_lifecycle_cleanup_jobs"
	LifecycleLegalHoldsTable  = "_lifecycle_legal_holds"
)

var tableOwnership = []schemaownership.Table{
	{
		Name: LifecycleCleanupJobsTable, Owner: MigrationOwner, WorkspaceScope: schemaownership.ScopeWorkspace,
		RetentionClass: schemaownership.RetentionTechnicalTTL, PrimaryKey: []string{"workspace_id", "id"},
		BoundedQueryPath: "workspace/job identity, bounded global runnable-job claim ordered by update time, and workspace status/lease indexes",
		DeletionPolicy:   "active and replayable jobs are retained; terminal job history becomes purge-eligible only after its configured replay and operational retention window",
	},
	{
		Name: LifecycleLegalHoldsTable, Owner: MigrationOwner, WorkspaceScope: schemaownership.ScopeWorkspace,
		RetentionClass: schemaownership.RetentionLegalAudit, PrimaryKey: []string{"workspace_id", "id"},
		BoundedQueryPath: "workspace/hold identity, bounded workspace listing, and exact owner/resource active-hold lookup",
		DeletionPolicy:   "ending a hold records its end time; active holds are never deleted and ended rows remain governed legal evidence until their legal-audit retention permits purge",
	},
}

func SchemaOwnership() []schemaownership.Table {
	return schemaownership.Clone(tableOwnership)
}

func OwnedTables() []string {
	return schemaownership.Names(SchemaOwnership())
}
