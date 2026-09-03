package lifecycle

import (
	"context"
	"slices"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
)

func TestLifecycleDataScopeCompilesOnlyCanonicalScopeForExactPermission(t *testing.T) {
	const permission = "lifecycle.subject_requests.preview"
	tests := []struct {
		name         string
		scope        identitysdk.DataScope
		unrestricted bool
		ownerUserIDs []string
		ownerOrgIDs  []string
	}{
		{name: "all", scope: identitysdk.DataScopeAll, unrestricted: true},
		{name: "owner", scope: identitysdk.DataScopeOwner, ownerUserIDs: []string{"user-a"}},
		{name: "org", scope: identitysdk.DataScopeOrg, ownerOrgIDs: []string{"org-a"}},
		{name: "org_child", scope: identitysdk.DataScopeOrgChild, ownerOrgIDs: []string{"org-a", "org-child"}},
		{name: "target_org", scope: identitysdk.DataScopeTargetOrg, ownerOrgIDs: []string{"org-target", "org-target-child"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			principal := lifecycleaccess.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "user-a", Permissions: map[string]struct{}{permission: {}}}
			ctx := identityScopeTestContext(t.Context(), principal, permission, test.scope)
			filter, err := lifecycleDataScope(ctx, principal, permission)
			if err != nil {
				t.Fatal(err)
			}
			if filter.Unrestricted != test.unrestricted || !slices.Equal(filter.OwnerUserIDs, test.ownerUserIDs) || !slices.Equal(filter.OwnerOrgIDs, test.ownerOrgIDs) || filter.SubjectOrgID != "org-a" {
				t.Fatalf("filter=%#v", filter)
			}
		})
	}
}

func TestLifecycleDataScopeRequiresFunctionAndSameExactKeyPolicy(t *testing.T) {
	const requested = "lifecycle.subject_requests.preview"
	principal := lifecycleaccess.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "user-a", Permissions: map[string]struct{}{requested: {}}}

	wrongKey := identityScopeTestContext(t.Context(), principal, "lifecycle.subject_requests.approve", identitysdk.DataScopeAll)
	if _, err := lifecycleDataScope(wrongKey, principal, requested); err == nil {
		t.Fatal("another exact Permission's data scope authorized the requested Action")
	}

	withoutFunction := identityScopeTestContext(t.Context(), principal, requested, identitysdk.DataScopeOwner)
	identity, _ := identitysdk.RequestIdentityFromContext(withoutFunction)
	identity.Principal.AccessBundle.FunctionGrants = nil
	withoutFunction = identitysdk.WithRequestIdentity(context.Background(), identity)
	if _, err := lifecycleDataScope(withoutFunction, principal, requested); err == nil {
		t.Fatal("data policy without functional Permission authorized the Action")
	}

	withoutDataPolicy := identityScopeTestContext(t.Context(), principal, requested, identitysdk.DataScopeOwner)
	identity, _ = identitysdk.RequestIdentityFromContext(withoutDataPolicy)
	identity.Principal.AccessBundle.DataPolicies = nil
	withoutDataPolicy = identitysdk.WithRequestIdentity(context.Background(), identity)
	if _, err := lifecycleDataScope(withoutDataPolicy, principal, requested); err == nil {
		t.Fatal("functional Permission without same-key data policy authorized the Action")
	}

	invalidScope := identityScopeTestContext(t.Context(), principal, requested, identitysdk.DataScope("custom"))
	if _, err := lifecycleDataScope(invalidScope, principal, requested); err == nil {
		t.Fatal("non-canonical data scope was accepted")
	}
}

func identityScopeTestContext(ctx context.Context, principal lifecycleaccess.Principal, permission string, scope identitysdk.DataScope) context.Context {
	separator := strings.LastIndex(permission, ".")
	resource, action := permission[:separator], permission[separator+1:]
	bundle := &identitysdk.AccessBundle{
		Subject: identitysdk.Subject{
			WorkspaceID: identitysdk.WorkspaceID(principal.WorkspaceID), SubjectID: identitysdk.SubjectID(principal.UserID), OrgID: "org-a",
			OrgScopeIDs: []string{"org-child", "org-a"}, SupportOrgScopeIDs: []string{"org-target-child", "org-target"},
		},
		FunctionGrants: []identitysdk.FunctionGrant{{Resource: identitysdk.ResourceType(resource), Action: identitysdk.Action(action), Effect: identitysdk.EffectAllow}},
		DataPolicies:   []identitysdk.DataPolicy{{Key: "test-" + permission, Resource: identitysdk.ResourceType(resource), Action: identitysdk.Action(action), Effect: identitysdk.EffectAllow, DataScopes: []identitysdk.DataScope{scope}}},
	}
	identity := identitysdk.RequestIdentity{Principal: identitysdk.Principal{Known: true, WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, AccessBundle: bundle}}
	return identitysdk.WithRequestIdentity(ctx, identity)
}
