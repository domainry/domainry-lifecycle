package service

import (
	"fmt"
	"sort"
	"strings"
	"time"

	lifecyclemodel "github.com/domainry/domainry-lifecycle/internal/domain/lifecycle/model"
)

func ValidateRetentionPolicy(policy lifecyclemodel.RetentionPolicy) error {
	if strings.TrimSpace(policy.Key) == "" || strings.TrimSpace(policy.Version) == "" || strings.TrimSpace(policy.Owner) == "" {
		return fmt.Errorf("retention policy key, version, and owner are required")
	}
	switch policy.Class {
	case lifecyclemodel.RetentionClassProduct, lifecyclemodel.RetentionClassLegalAudit, lifecyclemodel.RetentionClassTechnical, lifecyclemodel.RetentionClassUserErase:
	default:
		return fmt.Errorf("unsupported retention class %q", policy.Class)
	}
	if policy.DefaultRetention <= 0 || policy.MinimumRetention <= 0 {
		return fmt.Errorf("retention durations must be positive")
	}
	if policy.DefaultRetention < policy.MinimumRetention {
		return fmt.Errorf("default retention cannot be shorter than minimum retention")
	}
	if policy.ReplayWindow > 0 && (policy.DefaultRetention < policy.ReplayWindow || policy.MinimumRetention < policy.ReplayWindow) {
		return fmt.Errorf("retention cannot be shorter than the declared replay window")
	}
	for status, retention := range policy.StatusRetention {
		if strings.TrimSpace(status) == "" || retention < policy.MinimumRetention {
			return fmt.Errorf("status retention must name a status group and meet the policy minimum")
		}
	}
	if policy.WorkspaceMayReduce && (policy.Class == lifecyclemodel.RetentionClassLegalAudit || containsSensitivity(policy.Sensitivity, lifecyclemodel.SensitivityAudit) || containsSensitivity(policy.Sensitivity, lifecyclemodel.SensitivitySecurity)) {
		return fmt.Errorf("legal, audit, and security retention cannot be reduced by a workspace")
	}
	if policy.Class == lifecyclemodel.RetentionClassUserErase && policy.EraseBehavior == lifecyclemodel.EraseBehaviorNotEligible {
		return fmt.Errorf("user erase policy must define delete or anonymize behavior")
	}
	if policy.BackupBehavior == "" || policy.EraseBehavior == "" {
		return fmt.Errorf("backup and erase behavior are required")
	}
	return nil
}

func EffectiveWorkspaceRetention(policy lifecyclemodel.RetentionPolicy, override lifecyclemodel.WorkspaceRetentionOverride) (time.Duration, error) {
	if err := ValidateRetentionPolicy(policy); err != nil {
		return 0, err
	}
	if strings.TrimSpace(override.WorkspaceID) == "" || override.Retention <= 0 {
		return 0, fmt.Errorf("workspace and positive retention are required")
	}
	if override.Retention < policy.DefaultRetention && !policy.WorkspaceMayReduce {
		return 0, fmt.Errorf("workspace retention cannot reduce policy default")
	}
	if override.Retention > policy.DefaultRetention && !policy.WorkspaceMayExtend {
		return 0, fmt.Errorf("workspace retention cannot extend policy default")
	}
	if override.Retention < policy.MinimumRetention {
		return 0, fmt.Errorf("workspace retention cannot be shorter than policy minimum")
	}
	return override.Retention, nil
}

func ValidatePolicyPublication(previous *lifecyclemodel.PolicyVersion, next lifecyclemodel.PolicyVersion) error {
	if err := ValidateRetentionPolicy(next.Policy); err != nil {
		return err
	}
	if next.Status != lifecyclemodel.PolicyStatusPublished || next.Revision <= 0 || strings.TrimSpace(next.PublishedBy) == "" || next.PublishedAt.IsZero() {
		return fmt.Errorf("published policy requires revision, publisher, and publication time")
	}
	if previous == nil {
		return nil
	}
	if next.Revision != previous.Revision+1 {
		return fmt.Errorf("policy revision must increase by one")
	}
	shortened := next.Policy.DefaultRetention < previous.Policy.DefaultRetention || next.Policy.MinimumRetention < previous.Policy.MinimumRetention || next.Policy.ReplayWindow < previous.Policy.ReplayWindow
	for status, retention := range previous.Policy.StatusRetention {
		if nextRetention, exists := next.Policy.StatusRetention[status]; !exists || nextRetention < retention {
			shortened = true
		}
	}
	if shortened && (strings.TrimSpace(next.ApprovalRef) == "" || strings.TrimSpace(next.ChangePlanRef) == "") {
		return fmt.Errorf("retention shortening requires approval and change plan")
	}
	return nil
}

func ValidateCleanupJob(job lifecyclemodel.CleanupJob) error {
	if strings.TrimSpace(job.ID) == "" || strings.TrimSpace(job.WorkspaceID) == "" || strings.TrimSpace(job.PolicyKey) == "" || strings.TrimSpace(job.PolicyVersion) == "" || strings.TrimSpace(job.RequestedBy) == "" || strings.TrimSpace(job.Reason) == "" {
		return fmt.Errorf("cleanup identity, workspace, policy, requester, and reason are required")
	}
	if job.Operation != lifecyclemodel.OperationArchive && job.Operation != lifecyclemodel.OperationPurge {
		return fmt.Errorf("cleanup operation must be archive or purge")
	}
	if job.Status == "" || job.CreatedAt.IsZero() || job.UpdatedAt.IsZero() {
		return fmt.Errorf("cleanup status and timestamps are required")
	}
	return nil
}

func TransitionSubjectRequest(current, next lifecyclemodel.SubjectRequest) error {
	if current.ID == "" || next.ID != current.ID || current.WorkspaceID == "" || next.WorkspaceID != current.WorkspaceID || next.UpdatedAt.IsZero() || next.UpdatedAt.Before(current.UpdatedAt) {
		return fmt.Errorf("subject request identity and monotonic update are required")
	}
	switch {
	case current.Status == lifecyclemodel.SubjectRequestPendingVerification && next.Status == lifecyclemodel.SubjectRequestVerified:
		if strings.TrimSpace(next.ResolvedIdentity) == "" || strings.TrimSpace(next.VerifiedBy) == "" || strings.TrimSpace(next.SecondFactorRef) == "" {
			return fmt.Errorf("subject verification requires resolved identity, verifier, and second factor")
		}
	case current.Status == lifecyclemodel.SubjectRequestVerified && next.Status == lifecyclemodel.SubjectRequestPreviewed:
		if len(next.ImpactPreview) == 0 {
			return fmt.Errorf("subject request preview evidence is required")
		}
	case current.Status == lifecyclemodel.SubjectRequestPreviewed && next.Status == lifecyclemodel.SubjectRequestApproved:
		if strings.TrimSpace(next.ApprovedBy) == "" || next.ApprovedBy == next.RequestedBy {
			return fmt.Errorf("subject request requires independent approval")
		}
	case (current.Status == lifecyclemodel.SubjectRequestApproved || current.Status == lifecyclemodel.SubjectRequestFailed) && next.Status == lifecyclemodel.SubjectRequestExecuting:
		if next.ExecutionAttempt != current.ExecutionAttempt+1 || !next.ExecutionLeaseEnd.After(next.UpdatedAt) || next.LastError != "" {
			return fmt.Errorf("subject execution requires a new leased attempt")
		}
	case current.Status == lifecyclemodel.SubjectRequestExecuting && next.Status == lifecyclemodel.SubjectRequestExecuting:
		if current.ExecutionLeaseEnd.IsZero() || next.UpdatedAt.Before(current.ExecutionLeaseEnd) || next.ExecutionAttempt != current.ExecutionAttempt+1 || !next.ExecutionLeaseEnd.After(next.UpdatedAt) || next.LastError != "" {
			return fmt.Errorf("subject execution lease is still active")
		}
	case current.Status == lifecyclemodel.SubjectRequestExecuting && next.Status == lifecyclemodel.SubjectRequestSucceeded:
		if strings.TrimSpace(next.ResultReference) == "" {
			return fmt.Errorf("successful subject request requires result evidence")
		}
		if next.Kind == lifecyclemodel.SubjectRequestExport && (next.DownloadExpiresAt.IsZero() || !next.DownloadExpiresAt.After(next.UpdatedAt)) {
			return fmt.Errorf("subject export requires expiring download")
		}
		if !next.ExecutionLeaseEnd.IsZero() {
			return fmt.Errorf("successful subject request must release its execution lease")
		}
	case current.Status == lifecyclemodel.SubjectRequestExecuting && next.Status == lifecyclemodel.SubjectRequestFailed:
		if strings.TrimSpace(next.LastError) == "" {
			return fmt.Errorf("failed subject request requires error evidence")
		}
		if !next.ExecutionLeaseEnd.IsZero() {
			return fmt.Errorf("failed subject request must release its execution lease")
		}
	default:
		return fmt.Errorf("invalid subject request transition %s -> %s", current.Status, next.Status)
	}
	return nil
}

func EvaluateEligibility(input lifecyclemodel.EligibilityInput) (lifecyclemodel.EligibilityDecision, error) {
	if input.Operation != lifecyclemodel.OperationArchive && input.Operation != lifecyclemodel.OperationPurge && input.Operation != lifecyclemodel.OperationErase {
		return lifecyclemodel.EligibilityDecision{}, fmt.Errorf("unsupported lifecycle operation %q", input.Operation)
	}
	if strings.TrimSpace(input.Target.WorkspaceID) == "" || strings.TrimSpace(input.Target.Owner) == "" || strings.TrimSpace(input.Target.ResourceType) == "" || strings.TrimSpace(input.Target.ResourceID) == "" {
		return lifecyclemodel.EligibilityDecision{}, fmt.Errorf("workspace, owner, resource type, and resource id are required")
	}
	if input.Now.IsZero() {
		return lifecyclemodel.EligibilityDecision{}, fmt.Errorf("decision time is required")
	}
	blockers := make([]string, 0, 6)
	if !input.OwnerEligible {
		blockers = append(blockers, "owner_policy")
	}
	if input.ActiveProcess {
		blockers = append(blockers, "active_process")
	}
	if input.PendingOutbox {
		blockers = append(blockers, "pending_outbox")
	}
	if input.Referenced {
		blockers = append(blockers, "reference")
	}
	if input.BackupPolicyBlocked {
		blockers = append(blockers, "backup_policy")
	}
	for _, hold := range input.LegalHolds {
		if err := ValidateLegalHold(hold); err != nil {
			return lifecyclemodel.EligibilityDecision{}, err
		}
		if legalHoldApplies(hold, input.Target, input.Now) {
			blockers = append(blockers, "legal_hold:"+hold.ID)
		}
	}
	sort.Strings(blockers)
	return lifecyclemodel.EligibilityDecision{Eligible: len(blockers) == 0, Blockers: blockers}, nil
}

func ValidateLegalHold(hold lifecyclemodel.LegalHold) error {
	if strings.TrimSpace(hold.ID) == "" || strings.TrimSpace(hold.WorkspaceID) == "" || strings.TrimSpace(hold.Reason) == "" || strings.TrimSpace(hold.Authority) == "" || strings.TrimSpace(hold.AuditEvidence) == "" {
		return fmt.Errorf("legal hold id, workspace, reason, authority, and audit evidence are required")
	}
	if hold.StartsAt.IsZero() || hold.ReviewAt.IsZero() {
		return fmt.Errorf("legal hold start and review times are required")
	}
	if hold.EndsAt != nil && !hold.EndsAt.After(hold.StartsAt) {
		return fmt.Errorf("legal hold end must be after start")
	}
	return nil
}

func LegalHoldActive(hold lifecyclemodel.LegalHold, now time.Time) bool {
	return !now.Before(hold.StartsAt) && (hold.EndsAt == nil || now.Before(*hold.EndsAt))
}

func legalHoldApplies(hold lifecyclemodel.LegalHold, target lifecyclemodel.ResourceTarget, now time.Time) bool {
	if hold.WorkspaceID != target.WorkspaceID || now.Before(hold.StartsAt) || (hold.EndsAt != nil && !now.Before(*hold.EndsAt)) {
		return false
	}
	return (hold.Owner == "" || hold.Owner == target.Owner) && (hold.ResourceType == "" || hold.ResourceType == target.ResourceType) && (hold.ResourceID == "" || hold.ResourceID == target.ResourceID)
}

func DefaultPolicyCatalog(workspaceID, publisher string, now time.Time) []lifecyclemodel.PolicyVersion {
	day, year := 24*time.Hour, 365*24*time.Hour
	standard, delayed, locked := lifecyclemodel.BackupBehaviorStandard, lifecyclemodel.BackupBehaviorDelayedErase, lifecyclemodel.BackupBehaviorComplianceLocked
	deleteBehavior, anonymize, protected := lifecyclemodel.EraseBehaviorDelete, lifecyclemodel.EraseBehaviorAnonymize, lifecyclemodel.EraseBehaviorNotEligible
	entries := []struct {
		key, owner         string
		class              lifecyclemodel.RetentionClass
		retention, minimum time.Duration
		sensitivity        []lifecyclemodel.Sensitivity
		backup             lifecyclemodel.BackupBehavior
		erase              lifecyclemodel.EraseBehavior
	}{
		{"record.object.default.v1", "record", lifecyclemodel.RetentionClassProduct, year, 30 * day, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivitySensitive}, delayed, anonymize},
		{"audit.evidence.v1", "audit", lifecyclemodel.RetentionClassLegalAudit, 7 * year, year, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivityAudit}, locked, anonymize},
		{"workflow.definition.v1", "workflow", lifecyclemodel.RetentionClassProduct, 2 * year, year, nil, standard, protected},
		{"workflow.execution.v1", "workflow", lifecyclemodel.RetentionClassLegalAudit, 7 * year, year, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivityAudit}, locked, anonymize},
		{"workflow.receipt.v1", "workflow", lifecyclemodel.RetentionClassTechnical, 30 * day, 7 * day, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivitySecurity}, standard, deleteBehavior},
		{"automation.execution.v1", "automation", lifecyclemodel.RetentionClassProduct, year, 90 * day, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivitySensitive}, standard, anonymize},
		{"scheduler.execution.v1", "scheduler", lifecyclemodel.RetentionClassProduct, year, 90 * day, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivityAudit}, standard, anonymize},
		{"execution.idempotency_receipt.v1", "action", lifecyclemodel.RetentionClassTechnical, 30 * day, 7 * day, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivitySecurity}, standard, deleteBehavior},
		{"operations.receipt.v1", "operations", lifecyclemodel.RetentionClassLegalAudit, 7 * year, 90 * day, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivityAudit, lifecyclemodel.SensitivitySecurity}, locked, protected},
		{"operations.control.v1", "operations", lifecyclemodel.RetentionClassLegalAudit, 7 * year, year, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivityAudit, lifecyclemodel.SensitivitySecurity}, locked, protected},
		{"operations.break_glass.v1", "operations", lifecyclemodel.RetentionClassLegalAudit, 7 * year, year, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivityAudit, lifecyclemodel.SensitivitySecurity}, locked, protected},
		{"runtime.publication_handoff.v1", "runtime_handoff", lifecyclemodel.RetentionClassLegalAudit, year, 90 * day, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivityAudit}, locked, anonymize},
		{"notification.history.v1", "notification", lifecyclemodel.RetentionClassProduct, 180 * day, 30 * day, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivityPII}, standard, anonymize},
		{"notification.publication_history.v1", "notification", lifecyclemodel.RetentionClassProduct, 2 * year, 90 * day, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivityAudit}, standard, protected},
		{"localization.text.v1", "localization", lifecyclemodel.RetentionClassProduct, year, 30 * day, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivityPII}, standard, anonymize},
		{"runtime.configuration.v1", "capability", lifecyclemodel.RetentionClassProduct, year, 90 * day, nil, standard, protected},
		{"persistence.migration_evidence.v1", "deployment", lifecyclemodel.RetentionClassLegalAudit, 100 * year, 100 * year, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivityAudit}, locked, protected},
		{"ratelimit.bucket.v1", "runtime_security", lifecyclemodel.RetentionClassTechnical, day, time.Hour, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivitySecurity}, standard, deleteBehavior},
		{"agent.dialog.v1", "agent", lifecyclemodel.RetentionClassProduct, year, 30 * day, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivityPII}, delayed, anonymize},
		{"report.download.v1", "report", lifecyclemodel.RetentionClassTechnical, 7 * day, day, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivityPII}, standard, deleteBehavior},
		{"report.export.v1", "report", lifecyclemodel.RetentionClassLegalAudit, year, 90 * day, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivityPII, lifecyclemodel.SensitivityAudit}, delayed, anonymize},
		{"file.upload.v1", "upload", lifecyclemodel.RetentionClassProduct, year, day, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivityPII}, delayed, deleteBehavior},
		{"cache.dictionary.v1", "record", lifecyclemodel.RetentionClassTechnical, 15 * time.Minute, time.Minute, nil, standard, deleteBehavior},
		{"cache.runtime_projection.v1", "metadata", lifecyclemodel.RetentionClassTechnical, 15 * time.Minute, time.Minute, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivitySecurity}, standard, deleteBehavior},
	}
	result := make([]lifecyclemodel.PolicyVersion, 0, len(entries))
	for _, entry := range entries {
		statusRetention := map[string]time.Duration{}
		replayWindow := time.Duration(0)
		switch entry.key {
		case "workflow.receipt.v1", "execution.idempotency_receipt.v1":
			replayWindow = 7 * day
		case "workflow.execution.v1":
			statusRetention = map[string]time.Duration{"succeeded": 7 * year, "failed": 7 * year}
		case "automation.execution.v1":
			statusRetention = map[string]time.Duration{"succeeded": year, "failed": year}
		case "scheduler.execution.v1":
			statusRetention = map[string]time.Duration{"succeeded": 180 * day, "failed": year, "dead_letter": year}
		case "report.export.v1":
			statusRetention = map[string]time.Duration{"succeeded": year, "failed": year}
		}
		result = append(result, lifecyclemodel.PolicyVersion{WorkspaceID: workspaceID, Policy: lifecyclemodel.RetentionPolicy{Key: entry.key, Version: "1", Owner: entry.owner, Class: entry.class, Sensitivity: entry.sensitivity, DefaultRetention: entry.retention, MinimumRetention: entry.minimum, StatusRetention: statusRetention, ReplayWindow: replayWindow, WorkspaceMayExtend: true, LegalHoldEligible: entry.class != lifecyclemodel.RetentionClassTechnical, BackupBehavior: entry.backup, EraseBehavior: entry.erase}, Status: lifecyclemodel.PolicyStatusPublished, Revision: 1, PublishedBy: publisher, PublishedAt: now})
	}
	return result
}

func containsSensitivity(values []lifecyclemodel.Sensitivity, target lifecyclemodel.Sensitivity) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
