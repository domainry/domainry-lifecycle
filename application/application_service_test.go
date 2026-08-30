package application

import (
	"context"
	"testing"
	"time"

	"github.com/domainry/domainry-lifecycle/access"
	model "github.com/domainry/domainry-lifecycle/model"
	policy "github.com/domainry/domainry-lifecycle/policy"
)

type policyMemory struct{ values []model.PolicyVersion }

func (m *policyMemory) SavePolicy(_ context.Context, value model.PolicyVersion) error {
	m.values = append(m.values, value)
	return nil
}
func (m *policyMemory) LatestPolicy(_ context.Context, workspaceID, key string) (model.PolicyVersion, bool, error) {
	for index := len(m.values) - 1; index >= 0; index-- {
		if m.values[index].WorkspaceID == workspaceID && m.values[index].Policy.Key == key {
			return m.values[index], true, nil
		}
	}
	return model.PolicyVersion{}, false, nil
}
func (m *policyMemory) ListPolicies(_ context.Context, workspaceID string) ([]model.PolicyVersion, error) {
	result := []model.PolicyVersion{}
	for _, value := range m.values {
		if value.WorkspaceID == workspaceID {
			result = append(result, value)
		}
	}
	return result, nil
}

type evidenceMemory struct{ values []model.AuditEvidence }

func (*evidenceMemory) ListArchiveEntries(context.Context, string, string, int) ([]model.ArchiveEntry, error) {
	return nil, nil
}
func (m *evidenceMemory) AppendAuditEvidence(_ context.Context, value model.AuditEvidence) error {
	m.values = append(m.values, value)
	return nil
}
func (*evidenceMemory) Metrics(context.Context, string, time.Time) (model.Metrics, error) {
	return model.Metrics{}, nil
}
func (*evidenceMemory) GlobalMetrics(context.Context, access.SystemScope, time.Time) (model.Metrics, error) {
	return model.Metrics{}, nil
}

func TestPolicyUseCaseIsHostNeutralAndAudited(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	policies, evidence := &policyMemory{}, &evidenceMemory{}
	service := NewLifecycleApplicationService(t.Context(), LifecycleApplicationDependencies{Policies: policies, Evidence: evidence})
	principal := access.Principal{UserID: "admin", WorkspaceID: "workspace-a", Known: true, Permissions: map[string]struct{}{PermissionPolicyManage: {}}}
	value := policy.DefaultPolicyCatalog("workspace-a", "admin", now)[0]
	created, err := service.PublishPolicy(t.Context(), value, principal)
	if err != nil {
		t.Fatal(err)
	}
	if created.WorkspaceID != "workspace-a" || len(policies.values) != 1 || len(evidence.values) != 1 || evidence.values[0].Event != "lifecycle.policy.published" {
		t.Fatalf("policy=%#v policies=%d evidence=%#v", created, len(policies.values), evidence.values)
	}
	listed, err := service.ListPolicies(t.Context(), principal)
	if err != nil || len(listed) != 1 {
		t.Fatalf("listed=%#v err=%v", listed, err)
	}
	if _, err := service.ListPolicies(t.Context(), access.Principal{UserID: "reader", WorkspaceID: "workspace-a", Known: true}); err == nil {
		t.Fatal("permission-less principal was authorized")
	}
}

func TestWorkerRunnerRejectsMissingService(t *testing.T) {
	if _, err := NewWorkerRunner(nil).Tick(t.Context(), WorkerTick{}); err == nil {
		t.Fatal("nil Lifecycle service was accepted")
	}
}
