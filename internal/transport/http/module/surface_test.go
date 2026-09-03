package modulehttptransport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/modulehttp"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
)

type surfaceGovernanceStub struct {
	lifecyclesdk.Governance
	policy    lifecyclemodel.PolicyVersion
	principal lifecycleaccess.Principal
}

func (s *surfaceGovernanceStub) PublishPolicy(_ context.Context, policy lifecyclemodel.PolicyVersion, principal lifecycleaccess.Principal) (lifecyclemodel.PolicyVersion, error) {
	s.policy, s.principal = policy, principal
	policy.WorkspaceID, policy.PublishedBy, policy.Status = principal.WorkspaceID, principal.UserID, lifecyclemodel.PolicyStatusPublished
	return policy, nil
}

func TestLifecycleSurfaceOwnsProductRoutesAndOpenAPI(t *testing.T) {
	surface, err := NewSurface(&surfaceGovernanceStub{})
	if err != nil {
		t.Fatal(err)
	}
	if err := modulehttp.ValidateSurface(surface); err != nil {
		t.Fatal(err)
	}
	if len(surface.Routes()) != 17 {
		t.Fatalf("routes=%d", len(surface.Routes()))
	}
	provider := surface.(modulehttp.OpenAPIProvider)
	if len(provider.OpenAPIOperations()) != len(surface.Routes()) {
		t.Fatalf("OpenAPI operations=%d routes=%d", len(provider.OpenAPIOperations()), len(surface.Routes()))
	}
	for _, route := range surface.Routes() {
		if route.Pattern() == "POST /operations/lifecycle/cleanup/jobs/{jobID}/run" {
			t.Fatal("Runtime-owned durable cleanup execution leaked into module Surface")
		}
		if route.Action.AuditClass == "" || route.Action.IdempotencyDecision == "" {
			t.Fatalf("route lacks governance: %s", route.Pattern())
		}
	}
}

func TestLifecycleSurfaceIgnoresServerOwnedPolicyFields(t *testing.T) {
	governance := &surfaceGovernanceStub{}
	surface, err := NewSurface(governance)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/operations/lifecycle/policies", strings.NewReader(`{"policy":{"key":"records","version":"1","owner":"record"},"revision":2}`))
	request = request.WithContext(identitysdk.WithRequestIdentity(request.Context(), identitysdk.RequestIdentity{Principal: identitysdk.Principal{
		Known: true, WorkspaceID: "workspace-a", UserID: "operator-a",
		AccessBundle: &identitysdk.AccessBundle{
			Subject: identitysdk.Subject{WorkspaceID: "workspace-a", SubjectID: "operator-a"},
			FunctionGrants: []identitysdk.FunctionGrant{
				{Resource: identitysdk.ResourceType("lifecycle.policies"), Action: identitysdk.Action("publish"), Effect: identitysdk.EffectAllow},
				{Resource: identitysdk.ResourceType("lifecycle.policies"), Action: identitysdk.Action("list"), Effect: identitysdk.EffectAllow},
			},
			DataPolicies: []identitysdk.DataPolicy{
				{Key: "publish", Resource: identitysdk.ResourceType("lifecycle.policies"), Action: identitysdk.Action("publish"), Effect: identitysdk.EffectAllow, DataScopes: []identitysdk.DataScope{identitysdk.DataScopeOwner}},
				{Key: "list", Resource: identitysdk.ResourceType("lifecycle.policies"), Action: identitysdk.Action("list"), Effect: identitysdk.EffectAllow, DataScopes: []identitysdk.DataScope{identitysdk.DataScopeAll}},
			},
		},
	}}))
	response := httptest.NewRecorder()
	surface.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if governance.policy.WorkspaceID != "" || governance.policy.Status != "" || !governance.policy.PublishedAt.IsZero() || governance.principal.WorkspaceID != "workspace-a" {
		t.Fatalf("policy=%#v principal=%#v", governance.policy, governance.principal)
	}
	if !governance.principal.HasPermission(lifecyclesdk.ActionLifecyclePoliciesPublish) || governance.principal.HasPermission(lifecyclesdk.ActionLifecyclePoliciesList) {
		t.Fatalf("HTTP boundary did not narrow authority to the current Action: %#v", governance.principal.Permissions)
	}
	var result lifecyclemodel.PolicyVersion
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.WorkspaceID != "workspace-a" || result.PublishedBy != "operator-a" {
		t.Fatalf("result=%#v err=%v", result, err)
	}

	invalid := httptest.NewRecorder()
	badRequest := httptest.NewRequest(http.MethodPost, "/operations/lifecycle/policies", strings.NewReader(`{"policy":{},"published_by":"client"}`))
	surface.Handler().ServeHTTP(invalid, badRequest)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("server-owned field accepted: status=%d body=%s", invalid.Code, invalid.Body.String())
	}
}
