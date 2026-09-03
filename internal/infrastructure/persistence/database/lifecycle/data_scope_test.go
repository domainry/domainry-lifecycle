package lifecycle

import (
	"strings"
	"testing"

	lifecyclepersistence "github.com/domainry/domainry-lifecycle/internal/domain/lifecycle/repository"
	ormdialect "github.com/domainry/domainry-orm/dialect"
	"github.com/domainry/domainry-orm/query"
)

func TestAllDataScopeAddsNoRangePredicate(t *testing.T) {
	renderer, err := ormdialect.New(ormdialect.SQLite)
	if err != nil {
		t.Fatal(err)
	}
	builder := query.NewWorkspaceSelectBuilder(renderer.WithSchema(""), "_lifecycle_policy_versions", "workspace-a").Columns("payload_json")
	if predicate := dataScopePredicate(lifecyclepersistence.UnrestrictedDataScopeFilter(), "published_by", "owner_org_id"); predicate != nil {
		t.Fatal("all data scope produced a range predicate")
	}
	statement, _, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(statement, `"workspace_id" = ?`) || strings.Contains(statement, "published_by") || strings.Contains(statement, "owner_org_id") {
		t.Fatalf("all scope SQL=%s", statement)
	}
	store := LifecycleStore{renderer: renderer.WithSchema("")}
	if store.subjectRequestScopePredicate("workspace-a", lifecyclepersistence.UnrestrictedDataScopeFilter()) != nil || store.cleanupJobScopePredicate("workspace-a", lifecyclepersistence.UnrestrictedDataScopeFilter()) != nil {
		t.Fatal("all data scope produced a related-resource subquery")
	}
}

func TestBoundedDataScopeRendersOwnerAndOrganizationInSQL(t *testing.T) {
	renderer, err := ormdialect.New(ormdialect.SQLite)
	if err != nil {
		t.Fatal(err)
	}
	filter := lifecyclepersistence.DataScopeFilter{OwnerUserIDs: []string{"user-a"}, OwnerOrgIDs: []string{"org-a", "org-child"}}
	statement, args, err := query.NewWorkspaceSelectBuilder(renderer.WithSchema(""), "_lifecycle_subject_requests", "workspace-a").Columns("payload_json").
		Where(dataScopePredicate(filter, "requested_by", "owner_org_id")).Build()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(statement, `"requested_by" IN (?)`) || !strings.Contains(statement, `"owner_org_id" IN (?, ?)`) || len(args) != 4 {
		t.Fatalf("bounded SQL=%s args=%v", statement, args)
	}
}
