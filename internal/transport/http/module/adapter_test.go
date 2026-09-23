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

func TestLifecycleSurfaceOwnsProductRoutes(t *testing.T) {
	adapter, err := NewAdapter(&surfaceGovernanceStub{})
	if err != nil {
		t.Fatal(err)
	}
	if err := modulehttp.ValidateAdapter(adapter); err != nil {
		t.Fatal(err)
	}
	if len(adapter.Routes()) != 20 {
		t.Fatalf("routes=%d", len(adapter.Routes()))
	}
	for _, route := range adapter.Routes() {
		if route.Pattern() == "POST /lifecycle/cleanup/jobs/{jobID}/run" {
			t.Fatal("Runtime-owned durable cleanup execution leaked into module Adapter")
		}
		if route.Action.AuditClass == "" || route.Action.IdempotencyDecision == "" {
			t.Fatalf("route lacks governance: %s", route.Pattern())
		}
	}
}

func TestSubjectResponseRedactionKeepsAuthorizedImpactEvidence(t *testing.T) {
	request := lifecyclemodel.SubjectRequest{
		SubjectID: "subject-1", ResolvedIdentity: "identity-1", SecondFactorRef: "mfa-1",
		ResultReference: "artifact-1", ImpactPreview: json.RawMessage(`{"records":12}`),
	}
	detail := sanitizedSubject(request, true)
	if detail.SubjectID != "" || detail.ResolvedIdentity != "" || detail.SecondFactorRef != "" || detail.ResultReference != "" {
		t.Fatalf("sensitive subject fields were not redacted: %#v", detail)
	}
	if string(detail.ImpactPreview) != `{"records":12}` {
		t.Fatalf("authorized impact evidence was removed: %s", detail.ImpactPreview)
	}
	listItem := sanitizedSubject(request, false)
	if len(listItem.ImpactPreview) != 0 {
		t.Fatalf("list response exposed impact evidence: %s", listItem.ImpactPreview)
	}
}

func TestLifecycleSurfaceIgnoresServerOwnedPolicyFields(t *testing.T) {
	governance := &surfaceGovernanceStub{}
	adapter, err := NewAdapter(governance)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/lifecycle/policies", strings.NewReader(`{"policy":{"key":"records","version":"1","owner":"record"},"revision":2}`))
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
	adapter.Handler().ServeHTTP(response, request)
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
	badRequest := httptest.NewRequest(http.MethodPost, "/lifecycle/policies", strings.NewReader(`{"policy":{},"published_by":"client"}`))
	adapter.Handler().ServeHTTP(invalid, badRequest)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("server-owned field accepted: status=%d body=%s", invalid.Code, invalid.Body.String())
	}
}
