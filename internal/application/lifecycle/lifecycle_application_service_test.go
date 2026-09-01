package lifecycle

import (
	"context"
	"testing"
	"time"

	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
	lifecyclemodel "github.com/domainry/domainry-lifecycle/internal/domain/lifecycle/model"
)

type policyMemory struct {
	values []lifecyclemodel.PolicyVersion
}

func (m *policyMemory) SavePolicy(_ context.Context, value lifecyclemodel.PolicyVersion) error {
	m.values = append(m.values, value)
	return nil
}
func (m *policyMemory) LatestPolicy(_ context.Context, workspaceID, key string) (lifecyclemodel.PolicyVersion, bool, error) {
	for index := len(m.values) - 1; index >= 0; index-- {
		if m.values[index].WorkspaceID == workspaceID && m.values[index].Policy.Key == key {
			return m.values[index], true, nil
		}
	}
	return lifecyclemodel.PolicyVersion{}, false, nil
}
func (m *policyMemory) ListPolicies(_ context.Context, workspaceID string) ([]lifecyclemodel.PolicyVersion, error) {
	var result []lifecyclemodel.PolicyVersion
	for _, value := range m.values {
		if value.WorkspaceID == workspaceID {
			result = append(result, value)
		}
	}
	return result, nil
}

type evidenceMemory struct {
	values []lifecyclemodel.AuditEvidence
}

func (*evidenceMemory) ListArchiveEntries(context.Context, string, string, int) ([]lifecyclemodel.ArchiveEntry, error) {
	return nil, nil
}
func (m *evidenceMemory) AppendAuditEvidence(_ context.Context, value lifecyclemodel.AuditEvidence) error {
	m.values = append(m.values, value)
	return nil
}
func (*evidenceMemory) Metrics(context.Context, string, time.Time) (lifecyclemodel.Metrics, error) {
	return lifecyclemodel.Metrics{}, nil
}
func (*evidenceMemory) GlobalMetrics(context.Context, lifecycleaccess.SystemScope, time.Time) (lifecyclemodel.Metrics, error) {
	return lifecyclemodel.Metrics{}, nil
}

func TestPolicyUseCaseOwnsServerFieldsAuthorizationAndAudit(t *testing.T) {
	policies, evidence := &policyMemory{}, &evidenceMemory{}
	service := NewLifecycleApplicationService(t.Context(), LifecycleApplicationDependencies{Policies: policies, Evidence: evidence})
	principal := lifecycleaccess.Principal{UserID: "admin", WorkspaceID: "workspace-a", Known: true, Permissions: map[string]struct{}{lifecyclesdk.ActionLifecyclePoliciesPublish: {}}}
	value := lifecyclemodel.PolicyVersion{Policy: lifecyclemodel.RetentionPolicy{Key: "record.default", Version: "1", Owner: "record", Class: lifecyclemodel.RetentionClassProduct, DefaultRetention: 24 * time.Hour, MinimumRetention: time.Hour, BackupBehavior: lifecyclemodel.BackupBehaviorStandard, EraseBehavior: lifecyclemodel.EraseBehaviorDelete}}
	created, err := service.PublishPolicy(t.Context(), value, principal)
	if err != nil {
		t.Fatal(err)
	}
	if created.WorkspaceID != "workspace-a" || created.PublishedBy != "admin" || created.Status != lifecyclemodel.PolicyStatusPublished || created.Revision != 1 || created.PublishedAt.IsZero() || len(evidence.values) != 1 {
		t.Fatalf("created=%#v evidence=%#v", created, evidence.values)
	}
	if _, err := service.ListPolicies(t.Context(), principal); err == nil {
		t.Fatal("publish Action grant also authorized the distinct list Action")
	}
	if _, err := service.ListPolicies(t.Context(), lifecycleaccess.Principal{Known: true, WorkspaceID: "workspace-a"}); err == nil {
		t.Fatal("permission-less principal was authorized")
	}
}

func TestWorkerRunnerRejectsMissingService(t *testing.T) {
	if _, err := NewWorkerRunner(nil).Tick(t.Context(), WorkerTick{}); err == nil {
		t.Fatal("nil Lifecycle service was accepted")
	}
}
