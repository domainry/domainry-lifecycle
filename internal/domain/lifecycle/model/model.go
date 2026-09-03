package model

import (
	"encoding/json"
	"time"
)

type Operation string

const (
	OperationArchive Operation = "archive"
	OperationPurge   Operation = "purge"
	OperationErase   Operation = "erase"
)

type ResourceTarget struct {
	WorkspaceID  string `json:"workspace_id"`
	Owner        string `json:"owner,omitempty"`
	ResourceType string `json:"resource_type,omitempty"`
	ResourceID   string `json:"resource_id,omitempty"`
}

type LegalHold struct {
	ID            string     `json:"id"`
	WorkspaceID   string     `json:"workspace_id"`
	Owner         string     `json:"owner,omitempty"`
	ResourceType  string     `json:"resource_type,omitempty"`
	ResourceID    string     `json:"resource_id,omitempty"`
	Reason        string     `json:"reason"`
	Authority     string     `json:"authority"`
	StartsAt      time.Time  `json:"starts_at"`
	EndsAt        *time.Time `json:"ends_at,omitempty"`
	ReviewAt      time.Time  `json:"review_at"`
	AuditEvidence string     `json:"audit_evidence"`
	CreatedBy     string     `json:"created_by"`
	OwnerOrgID    string     `json:"owner_org_id,omitempty"`
}

type EligibilityInput struct {
	Operation           Operation
	Target              ResourceTarget
	OwnerEligible       bool
	ActiveProcess       bool
	PendingOutbox       bool
	Referenced          bool
	BackupPolicyBlocked bool
	LegalHolds          []LegalHold
	Now                 time.Time
}

type EligibilityDecision struct {
	Eligible bool
	Blockers []string
}

type PolicyStatus string

const (
	PolicyStatusDraft     PolicyStatus = "draft"
	PolicyStatusPublished PolicyStatus = "published"
	PolicyStatusRetired   PolicyStatus = "retired"
)

type PolicyVersion struct {
	WorkspaceID    string          `json:"workspace_id"`
	Policy         RetentionPolicy `json:"policy"`
	Status         PolicyStatus    `json:"status"`
	Revision       int64           `json:"revision"`
	PublishedBy    string          `json:"published_by"`
	OwnerOrgID     string          `json:"owner_org_id,omitempty"`
	PublishedAt    time.Time       `json:"published_at"`
	ApprovalRef    string          `json:"approval_ref,omitempty"`
	EstimatedRows  int64           `json:"estimated_rows"`
	EstimatedBytes int64           `json:"estimated_bytes"`
	ChangePlanRef  string          `json:"change_plan_ref,omitempty"`
}

type CleanupStatus string

const (
	CleanupStatusPending   CleanupStatus = "pending"
	CleanupStatusRunning   CleanupStatus = "running"
	CleanupStatusPaused    CleanupStatus = "paused"
	CleanupStatusSucceeded CleanupStatus = "succeeded"
	CleanupStatusFailed    CleanupStatus = "failed"
)

type CleanupJob struct {
	ID             string        `json:"id"`
	WorkspaceID    string        `json:"workspace_id"`
	PolicyKey      string        `json:"policy_key"`
	PolicyVersion  string        `json:"policy_version"`
	Operation      Operation     `json:"operation"`
	Status         CleanupStatus `json:"status"`
	DryRun         bool          `json:"dry_run"`
	Checkpoint     string        `json:"checkpoint,omitempty"`
	LeaseOwner     string        `json:"lease_owner,omitempty"`
	LeaseExpiresAt time.Time     `json:"lease_expires_at,omitempty"`
	FencingToken   int64         `json:"fencing_token"`
	EstimatedRows  int64         `json:"estimated_rows"`
	EstimatedBytes int64         `json:"estimated_bytes"`
	Scanned        int64         `json:"scanned"`
	Archived       int64         `json:"archived"`
	Purged         int64         `json:"purged"`
	Skipped        int64         `json:"skipped"`
	Failed         int64         `json:"failed"`
	OldestEligible time.Time     `json:"oldest_eligible,omitempty"`
	LastError      string        `json:"last_error,omitempty"`
	RequestedBy    string        `json:"requested_by"`
	OwnerOrgID     string        `json:"owner_org_id,omitempty"`
	Reason         string        `json:"reason"`
	CreatedAt      time.Time     `json:"created_at"`
	UpdatedAt      time.Time     `json:"updated_at"`
}

type CleanupBatchResult struct {
	Checkpoint     string    `json:"checkpoint,omitempty"`
	Scanned        int64     `json:"scanned"`
	Archived       int64     `json:"archived"`
	Purged         int64     `json:"purged"`
	Skipped        int64     `json:"skipped"`
	Failed         int64     `json:"failed"`
	OldestEligible time.Time `json:"oldest_eligible,omitempty"`
	Done           bool      `json:"done"`
}

type SubjectRequestKind string

const (
	SubjectRequestExport SubjectRequestKind = "export"
	SubjectRequestErase  SubjectRequestKind = "erase"
)

type SubjectRequestStatus string

const (
	SubjectRequestPendingVerification SubjectRequestStatus = "pending_verification"
	SubjectRequestVerified            SubjectRequestStatus = "verified"
	SubjectRequestPreviewed           SubjectRequestStatus = "previewed"
	SubjectRequestApproved            SubjectRequestStatus = "approved"
	SubjectRequestExecuting           SubjectRequestStatus = "executing"
	SubjectRequestSucceeded           SubjectRequestStatus = "succeeded"
	SubjectRequestFailed              SubjectRequestStatus = "failed"
)

type SubjectRequest struct {
	ID                string               `json:"id"`
	WorkspaceID       string               `json:"workspace_id"`
	Kind              SubjectRequestKind   `json:"kind"`
	Status            SubjectRequestStatus `json:"status"`
	SubjectType       string               `json:"subject_type"`
	SubjectID         string               `json:"subject_id"`
	ResolvedIdentity  string               `json:"resolved_identity,omitempty"`
	RequestedBy       string               `json:"requested_by"`
	OwnerOrgID        string               `json:"owner_org_id,omitempty"`
	VerifiedBy        string               `json:"verified_by,omitempty"`
	ApprovedBy        string               `json:"approved_by,omitempty"`
	SecondFactorRef   string               `json:"second_factor_ref,omitempty"`
	Reason            string               `json:"reason"`
	ImpactPreview     json.RawMessage      `json:"impact_preview,omitempty"`
	ResultReference   string               `json:"result_reference,omitempty"`
	DownloadExpiresAt time.Time            `json:"download_expires_at,omitempty"`
	BackupPending     bool                 `json:"backup_pending"`
	LastError         string               `json:"last_error,omitempty"`
	ExecutionAttempt  int64                `json:"execution_attempt"`
	ExecutionLeaseEnd time.Time            `json:"execution_lease_end,omitempty"`
	CreatedAt         time.Time            `json:"created_at"`
	UpdatedAt         time.Time            `json:"updated_at"`
}

type ExternalErasure struct {
	ID           string    `json:"id"`
	RequestID    string    `json:"request_id"`
	WorkspaceID  string    `json:"workspace_id"`
	ConnectorKey string    `json:"connector_key"`
	ProviderRef  string    `json:"provider_ref"`
	Status       string    `json:"status"`
	ReconciledAt time.Time `json:"reconciled_at,omitempty"`
	Evidence     string    `json:"evidence,omitempty"`
}

type DeletionRegistration struct {
	RequestID        string    `json:"request_id"`
	WorkspaceID      string    `json:"workspace_id"`
	ResolvedIdentity string    `json:"resolved_identity"`
	BackupPending    bool      `json:"backup_pending"`
	Evidence         string    `json:"evidence"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// SubjectExecutionStep is Lifecycle-owned recovery evidence for one
// idempotent owner side effect within a subject request.
type SubjectExecutionStep struct {
	WorkspaceID string          `json:"workspace_id"`
	RequestID   string          `json:"request_id"`
	Owner       string          `json:"owner"`
	Operation   string          `json:"operation"`
	Payload     json.RawMessage `json:"payload"`
	CompletedAt time.Time       `json:"completed_at"`
}

type ArchiveEntry struct {
	ID            string          `json:"id"`
	WorkspaceID   string          `json:"workspace_id"`
	Owner         string          `json:"owner"`
	SourceTable   string          `json:"source_table"`
	ResourceID    string          `json:"resource_id"`
	PolicyKey     string          `json:"policy_key"`
	PolicyVersion string          `json:"policy_version"`
	JobID         string          `json:"job_id"`
	PayloadHash   string          `json:"payload_hash"`
	Payload       json.RawMessage `json:"-"`
	ArchivedAt    time.Time       `json:"archived_at"`
}

type AuditEvidence struct {
	ID          string          `json:"id"`
	WorkspaceID string          `json:"workspace_id"`
	Event       string          `json:"event"`
	ActorID     string          `json:"actor_id"`
	ResourceID  string          `json:"resource_id"`
	PolicyKey   string          `json:"policy_key,omitempty"`
	Payload     json.RawMessage `json:"payload"`
	CreatedAt   time.Time       `json:"created_at"`
}

type Metrics struct {
	EligibleBacklog int64     `json:"eligible_backlog"`
	OldestEligible  time.Time `json:"oldest_eligible,omitempty"`
	PurgedTotal     int64     `json:"purged_total"`
	FailureTotal    int64     `json:"failure_total"`
	LegalHoldCount  int64     `json:"legal_hold_count"`
	Warning         bool      `json:"warning"`
}

type RetentionClass string

const (
	RetentionClassProduct    RetentionClass = "product_retention"
	RetentionClassLegalAudit RetentionClass = "legal_audit_retention"
	RetentionClassTechnical  RetentionClass = "technical_ttl"
	RetentionClassUserErase  RetentionClass = "user_requested_erase"
)

type Sensitivity string

const (
	SensitivityInternal  Sensitivity = "internal"
	SensitivityPII       Sensitivity = "pii"
	SensitivitySensitive Sensitivity = "sensitive"
	SensitivityFinancial Sensitivity = "financial"
	SensitivitySecurity  Sensitivity = "security"
	SensitivityAudit     Sensitivity = "audit"
)

type BackupBehavior string

const (
	BackupBehaviorStandard         BackupBehavior = "standard_restore_then_reconcile"
	BackupBehaviorDelayedErase     BackupBehavior = "delayed_erase_after_restore"
	BackupBehaviorComplianceLocked BackupBehavior = "compliance_locked"
)

type EraseBehavior string

const (
	EraseBehaviorDelete      EraseBehavior = "delete"
	EraseBehaviorAnonymize   EraseBehavior = "anonymize"
	EraseBehaviorNotEligible EraseBehavior = "not_eligible"
)

type RetentionPolicy struct {
	Key                     string                   `json:"key"`
	Version                 string                   `json:"version"`
	Owner                   string                   `json:"owner"`
	Class                   RetentionClass           `json:"class"`
	Sensitivity             []Sensitivity            `json:"sensitivity,omitempty"`
	DefaultRetention        time.Duration            `json:"default_retention"`
	MinimumRetention        time.Duration            `json:"minimum_retention"`
	StatusRetention         map[string]time.Duration `json:"status_retention,omitempty"`
	ReplayWindow            time.Duration            `json:"replay_window,omitempty"`
	WorkspaceMayExtend      bool                     `json:"workspace_may_extend"`
	WorkspaceMayReduce      bool                     `json:"workspace_may_reduce"`
	LegalHoldEligible       bool                     `json:"legal_hold_eligible"`
	BackupBehavior          BackupBehavior           `json:"backup_behavior"`
	EraseBehavior           EraseBehavior            `json:"erase_behavior"`
	RequiredReferenceChecks []string                 `json:"required_reference_checks,omitempty"`
}

type WorkspaceRetentionOverride struct {
	WorkspaceID string        `json:"workspace_id"`
	Retention   time.Duration `json:"retention"`
}
