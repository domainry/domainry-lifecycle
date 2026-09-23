package service

import (
	"encoding/json"
	"testing"
	"time"

	lifecyclemodel "github.com/domainry/domainry-lifecycle/internal/domain/lifecycle/model"
)

func TestRetentionPolicyAndPublicationGovernance(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	base := lifecyclemodel.RetentionPolicy{
		Key: "record.default", Version: "1", Owner: "record", Class: lifecyclemodel.RetentionClassProduct,
		DefaultRetention: 365 * 24 * time.Hour, MinimumRetention: 30 * 24 * time.Hour,
		WorkspaceMayExtend: true, LegalHoldEligible: true,
		BackupBehavior: lifecyclemodel.BackupBehaviorDelayedErase, EraseBehavior: lifecyclemodel.EraseBehaviorAnonymize,
	}
	previous := lifecyclemodel.PolicyVersion{Policy: base, Status: lifecyclemodel.PolicyStatusPublished, Revision: 1, PublishedBy: "admin", PublishedAt: now}
	next := previous
	next.Revision, next.Policy.Version, next.Policy.DefaultRetention, next.PublishedAt = 2, "2", 180*24*time.Hour, now.Add(time.Hour)
	if err := ValidatePolicyPublication(&previous, next); err == nil {
		t.Fatal("retention shortening without approval was accepted")
	}
	next.ApprovalRef, next.ChangePlanRef = "approval-1", "change-plan-1"
	if err := ValidatePolicyPublication(&previous, next); err != nil {
		t.Fatal(err)
	}
	replay := base
	replay.Class, replay.DefaultRetention, replay.MinimumRetention, replay.ReplayWindow = lifecyclemodel.RetentionClassTechnical, 24*time.Hour, time.Hour, 48*time.Hour
	replay.BackupBehavior, replay.EraseBehavior = lifecyclemodel.BackupBehaviorStandard, lifecyclemodel.EraseBehaviorDelete
	if err := ValidateRetentionPolicy(replay); err == nil {
		t.Fatal("retention shorter than replay window was accepted")
	}
}

func TestEligibilityAndLegalHoldStayWorkspaceScoped(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	target := lifecyclemodel.ResourceTarget{WorkspaceID: "workspace-a", Owner: "record", ResourceType: "customer", ResourceID: "customer-1"}
	hold := lifecyclemodel.LegalHold{ID: "hold-1", WorkspaceID: "workspace-a", Owner: "record", ResourceType: "customer", ResourceID: "customer-1", Reason: "litigation", Authority: "legal", StartsAt: now.Add(-time.Hour), ReviewAt: now.Add(time.Hour), AuditEvidence: "evidence-1"}
	decision, err := EvaluateEligibility(lifecyclemodel.EligibilityInput{Operation: lifecyclemodel.OperationPurge, Target: target, OwnerEligible: true, LegalHolds: []lifecyclemodel.LegalHold{hold}, Now: now})
	if err != nil || decision.Eligible || len(decision.Blockers) != 1 || decision.Blockers[0] != "legal_hold:hold-1" {
		t.Fatalf("decision=%#v err=%v", decision, err)
	}
	target.WorkspaceID = "workspace-b"
	decision, err = EvaluateEligibility(lifecyclemodel.EligibilityInput{Operation: lifecyclemodel.OperationPurge, Target: target, OwnerEligible: true, LegalHolds: []lifecyclemodel.LegalHold{hold}, Now: now})
	if err != nil || !decision.Eligible {
		t.Fatalf("legal hold crossed workspace: decision=%#v err=%v", decision, err)
	}
}

func TestSubjectTransitionRequiresIndependentApprovalAndExecutionEvidence(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	current := lifecyclemodel.SubjectRequest{ID: "request-1", WorkspaceID: "workspace-a", Kind: lifecyclemodel.SubjectRequestExport, Status: lifecyclemodel.SubjectRequestPendingVerification, RequestedBy: "requester", UpdatedAt: now}
	verified := current
	verified.Status, verified.ResolvedIdentity, verified.VerifiedBy, verified.SecondFactorRef, verified.UpdatedAt = lifecyclemodel.SubjectRequestVerified, "identity-1", "verifier", "mfa-1", now.Add(time.Minute)
	if err := TransitionSubjectRequest(current, verified); err != nil {
		t.Fatal(err)
	}
	previewed := verified
	previewed.Status, previewed.ImpactPreview, previewed.UpdatedAt = lifecyclemodel.SubjectRequestPreviewed, json.RawMessage(`{"owners":["record"]}`), now.Add(2*time.Minute)
	if err := TransitionSubjectRequest(verified, previewed); err != nil {
		t.Fatal(err)
	}
	approved := previewed
	approved.Status, approved.ApprovedBy, approved.UpdatedAt = lifecyclemodel.SubjectRequestApproved, "requester", now.Add(3*time.Minute)
	if err := TransitionSubjectRequest(previewed, approved); err == nil {
		t.Fatal("self approval was accepted")
	}
	approved.ApprovedBy = "approver"
	if err := TransitionSubjectRequest(previewed, approved); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultPolicyCatalogIsComplete(t *testing.T) {
	catalog := DefaultPolicyCatalog("workspace-a", "admin", time.Now().UTC())
	if len(catalog) != 25 {
		t.Fatalf("catalog entries=%d", len(catalog))
	}
	policies := map[string]lifecyclemodel.RetentionPolicy{}
	for _, version := range catalog {
		if err := ValidatePolicyPublication(nil, version); err != nil {
			t.Fatalf("policy %s: %v", version.Policy.Key, err)
		}
		policies[version.Policy.Key] = version.Policy
	}
	technical := policies["operations.technical_receipt.v1"]
	legal := policies["operations.receipt.v1"]
	if technical.Class != lifecyclemodel.RetentionClassTechnical || technical.DefaultRetention != 30*24*time.Hour || technical.ReplayWindow != 7*24*time.Hour {
		t.Fatalf("technical Operations policy=%#v", technical)
	}
	if legal.Class != lifecyclemodel.RetentionClassLegalAudit || legal.StatusRetention["succeeded"] != 365*24*time.Hour || legal.StatusRetention["failed"] != 7*365*24*time.Hour {
		t.Fatalf("legal Operations policy=%#v", legal)
	}
}
