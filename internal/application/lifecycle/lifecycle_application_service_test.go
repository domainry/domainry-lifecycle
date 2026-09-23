package lifecycle

import (
	"context"
	"strings"
	"testing"
	"time"

	auditcontract "github.com/domainry/domainry-audit-sdk/contract"
	"github.com/domainry/domainry-foundation/requestcontext"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
	lifecyclemodel "github.com/domainry/domainry-lifecycle/internal/domain/lifecycle/model"
	lifecyclepersistence "github.com/domainry/domainry-lifecycle/internal/domain/lifecycle/repository"
)

type policyMemory struct {
	values []lifecyclemodel.PolicyVersion
}

func (m *policyMemory) SavePolicy(_ context.Context, value lifecyclemodel.PolicyVersion) error {
	m.values = append(m.values, value)
	return nil
}
func (m *policyMemory) LatestPolicy(_ context.Context, workspaceID, key string, filter lifecyclepersistence.DataScopeFilter) (lifecyclemodel.PolicyVersion, bool, error) {
	for index := len(m.values) - 1; index >= 0; index-- {
		if value := m.values[index]; value.WorkspaceID == workspaceID && value.Policy.Key == key && filter.Allows(value.PublishedBy, value.OwnerOrgID) {
			return m.values[index], true, nil
		}
	}
	return lifecyclemodel.PolicyVersion{}, false, nil
}
func (m *policyMemory) ListPolicies(_ context.Context, workspaceID string, filter lifecyclepersistence.DataScopeFilter) ([]lifecyclemodel.PolicyVersion, error) {
	var result []lifecyclemodel.PolicyVersion
	for _, value := range m.values {
		if value.WorkspaceID == workspaceID && filter.Allows(value.PublishedBy, value.OwnerOrgID) {
			result = append(result, value)
		}
	}
	return result, nil
}

type evidenceMemory struct {
	values []auditcontract.AppendRequest
}

func (*evidenceMemory) ListArchiveEntries(context.Context, string, string, int, lifecyclepersistence.DataScopeFilter) ([]lifecyclemodel.ArchiveEntry, error) {
	return nil, nil
}
func (m *evidenceMemory) AppendComplianceEvent(_ context.Context, value auditcontract.AppendRequest) error {
	m.values = append(m.values, value)
	return nil
}
func (*evidenceMemory) Metrics(context.Context, string, time.Time, lifecyclepersistence.DataScopeFilter) (lifecyclemodel.Metrics, error) {
	return lifecyclemodel.Metrics{}, nil
}
func (*evidenceMemory) GlobalMetrics(context.Context, lifecycleaccess.SystemScope, time.Time) (lifecyclemodel.Metrics, error) {
	return lifecyclemodel.Metrics{}, nil
}

func withLifecycleIdentity(ctx context.Context, principal lifecycleaccess.Principal, permission string) context.Context {
	separator := strings.LastIndex(permission, ".")
	resource, action := permission[:separator], permission[separator+1:]
	return identitysdk.WithRequestIdentity(ctx, identitysdk.RequestIdentity{Principal: identitysdk.Principal{
		Known: true, WorkspaceID: principal.WorkspaceID, UserID: principal.UserID,
		AccessBundle: &identitysdk.AccessBundle{
			Subject:        identitysdk.Subject{WorkspaceID: identitysdk.WorkspaceID(principal.WorkspaceID), SubjectID: identitysdk.SubjectID(principal.UserID)},
			FunctionGrants: []identitysdk.FunctionGrant{{Resource: identitysdk.ResourceType(resource), Action: identitysdk.Action(action), Effect: identitysdk.EffectAllow}},
			DataPolicies:   []identitysdk.DataPolicy{{Resource: identitysdk.ResourceType(resource), Action: identitysdk.Action(action), Effect: identitysdk.EffectAllow, DataScopes: []identitysdk.DataScope{identitysdk.DataScopeOwner}}},
		},
	}})
}

func TestPolicyUseCaseOwnsServerFieldsAuthorizationAndAudit(t *testing.T) {
	policies, evidence := &policyMemory{}, &evidenceMemory{}
	service := NewLifecycleApplicationService(t.Context(), LifecycleApplicationDependencies{Policies: policies, Evidence: evidence, Compliance: evidence})
	principal := lifecycleaccess.Principal{UserID: "admin", WorkspaceID: "workspace-a", Known: true, Permissions: map[string]struct{}{lifecyclesdk.ActionLifecyclePoliciesPublish: {}}}
	ctx := withLifecycleIdentity(t.Context(), principal, lifecyclesdk.ActionLifecyclePoliciesPublish)
	value := lifecyclemodel.PolicyVersion{Policy: lifecyclemodel.RetentionPolicy{Key: "record.default", Version: "1", Owner: "record", Class: lifecyclemodel.RetentionClassProduct, DefaultRetention: 24 * time.Hour, MinimumRetention: time.Hour, BackupBehavior: lifecyclemodel.BackupBehaviorStandard, EraseBehavior: lifecyclemodel.EraseBehaviorDelete}}
	created, err := service.PublishPolicy(ctx, value, principal)
	if err != nil {
		t.Fatal(err)
	}
	if created.WorkspaceID != "workspace-a" || created.PublishedBy != "admin" || created.Status != lifecyclemodel.PolicyStatusPublished || created.Revision != 1 || created.PublishedAt.IsZero() || len(evidence.values) != 1 {
		t.Fatalf("created=%#v evidence=%#v", created, evidence.values)
	}
	if _, err := service.ListPolicies(ctx, principal); err == nil {
		t.Fatal("publish Action grant also authorized the distinct list Action")
	}
	if _, err := service.ListPolicies(t.Context(), lifecycleaccess.Principal{Known: true, WorkspaceID: "workspace-a"}); err == nil {
		t.Fatal("permission-less principal was authorized")
	}
	beforePolicies, beforeEvidence := len(policies.values), len(evidence.values)
	unauthenticated := lifecycleaccess.Principal{WorkspaceID: "workspace-a", Permissions: map[string]struct{}{lifecyclesdk.ActionLifecyclePoliciesPublish: {}}}
	if _, err := service.PublishPolicy(t.Context(), value, unauthenticated); err == nil {
		t.Fatal("unauthenticated principal published a policy")
	}
	if _, err := service.ListPolicies(t.Context(), unauthenticated); err == nil {
		t.Fatal("unauthenticated principal listed policies")
	}
	if len(policies.values) != beforePolicies || len(evidence.values) != beforeEvidence {
		t.Fatal("unauthenticated read/write attempt changed state")
	}
}

func TestLifecycleAuditSeparatesOperationAndOwnerRunIdentities(t *testing.T) {
	evidence := &evidenceMemory{}
	service := NewLifecycleApplicationService(t.Context(), LifecycleApplicationDependencies{Evidence: evidence, Compliance: evidence})
	job := lifecyclemodel.CleanupJob{ID: "cleanup-1", WorkspaceID: "workspace-a", OperationID: "operation-from-job"}
	ctx := requestcontext.WithOwnerExecutionID(t.Context(), "operation-context")
	if err := service.audit(ctx, job.WorkspaceID, "lifecycle.cleanup.succeeded", "worker", job.ID, "policy-1", job); err != nil {
		t.Fatal(err)
	}
	if len(evidence.values) != 1 || evidence.values[0].OperationID != "operation-context" || evidence.values[0].OwnerRunID != "cleanup-1" {
		t.Fatalf("correlated evidence=%#v", evidence.values)
	}
	if _, duplicated := evidence.values[0].Metadata["owner_run_id"]; duplicated {
		t.Fatalf("owner run identity was duplicated into metadata: %#v", evidence.values[0].Metadata)
	}
	if err := service.audit(t.Context(), job.WorkspaceID, "lifecycle.cleanup.succeeded", "worker", job.ID, "policy-1", job); err != nil {
		t.Fatal(err)
	}
	if evidence.values[1].OperationID != "operation-from-job" {
		t.Fatalf("durable cleanup operation identity=%q", evidence.values[1].OperationID)
	}
}

func TestPolicyPublishPrechecksCandidateWithinExactPermissionScope(t *testing.T) {
	policies, evidence := &policyMemory{}, &evidenceMemory{}
	service := NewLifecycleApplicationService(t.Context(), LifecycleApplicationDependencies{Policies: policies, Evidence: evidence, Compliance: evidence})
	owner := lifecycleaccess.Principal{UserID: "owner-a", WorkspaceID: "workspace-a", Known: true, Permissions: map[string]struct{}{lifecyclesdk.ActionLifecyclePoliciesPublish: {}}}
	base := lifecyclemodel.PolicyVersion{Policy: lifecyclemodel.RetentionPolicy{Key: "record.default", Version: "1", Owner: "record", Class: lifecyclemodel.RetentionClassProduct, DefaultRetention: 24 * time.Hour, MinimumRetention: time.Hour, BackupBehavior: lifecyclemodel.BackupBehaviorStandard, EraseBehavior: lifecyclemodel.EraseBehaviorDelete}}
	if _, err := service.PublishPolicy(withLifecycleIdentity(t.Context(), owner, lifecyclesdk.ActionLifecyclePoliciesPublish), base, owner); err != nil {
		t.Fatal(err)
	}
	other := lifecycleaccess.Principal{UserID: "owner-b", WorkspaceID: "workspace-a", Known: true, Permissions: map[string]struct{}{lifecyclesdk.ActionLifecyclePoliciesPublish: {}}}
	next := base
	next.Policy.Version = "2"
	if _, err := service.PublishPolicy(withLifecycleIdentity(t.Context(), other, lifecyclesdk.ActionLifecyclePoliciesPublish), next, other); err == nil {
		t.Fatal("owner-scoped actor published a version over another owner's policy")
	}
	if len(policies.values) != 1 || len(evidence.values) != 1 {
		t.Fatalf("denied publish changed state: policies=%d evidence=%d", len(policies.values), len(evidence.values))
	}
}

func TestWorkerRunnerRejectsMissingService(t *testing.T) {
	if _, err := NewWorkerRunner(nil).Tick(t.Context(), WorkerTick{}); err == nil {
		t.Fatal("nil Lifecycle service was accepted")
	}
}

func TestLifecycleAuditRegistryExcludesWorkerProgressAndFailureAttempts(t *testing.T) {
	for _, event := range []string{"lifecycle.cleanup.progressed", "lifecycle.cleanup.failed"} {
		if _, registered := lifecycleAuditEvents[event]; registered {
			t.Fatalf("technical worker state registered as Audit event: %s", event)
		}
	}
	for _, event := range []string{"lifecycle.cleanup.created", "lifecycle.cleanup.succeeded", "lifecycle.subject.succeeded"} {
		if _, registered := lifecycleAuditEvents[event]; !registered {
			t.Fatalf("compliance fact missing from Audit registry: %s", event)
		}
	}
}
