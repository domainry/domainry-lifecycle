// Package model is the source-owned Lifecycle domain-model boundary.
// Aliases preserve identity with the versioned SDK wire contract.
package model

import sdkmodel "github.com/domainry/domainry-lifecycle-sdk/model"

type (
	Operation                  = sdkmodel.Operation
	ResourceTarget             = sdkmodel.ResourceTarget
	LegalHold                  = sdkmodel.LegalHold
	EligibilityInput           = sdkmodel.EligibilityInput
	EligibilityDecision        = sdkmodel.EligibilityDecision
	PolicyStatus               = sdkmodel.PolicyStatus
	PolicyVersion              = sdkmodel.PolicyVersion
	CleanupStatus              = sdkmodel.CleanupStatus
	CleanupJob                 = sdkmodel.CleanupJob
	CleanupBatchResult         = sdkmodel.CleanupBatchResult
	SubjectRequestKind         = sdkmodel.SubjectRequestKind
	SubjectRequestStatus       = sdkmodel.SubjectRequestStatus
	SubjectRequest             = sdkmodel.SubjectRequest
	ExternalErasure            = sdkmodel.ExternalErasure
	DeletionRegistration       = sdkmodel.DeletionRegistration
	ArchiveEntry               = sdkmodel.ArchiveEntry
	AuditEvidence              = sdkmodel.AuditEvidence
	Metrics                    = sdkmodel.Metrics
	RetentionClass             = sdkmodel.RetentionClass
	Sensitivity                = sdkmodel.Sensitivity
	BackupBehavior             = sdkmodel.BackupBehavior
	EraseBehavior              = sdkmodel.EraseBehavior
	RetentionPolicy            = sdkmodel.RetentionPolicy
	WorkspaceRetentionOverride = sdkmodel.WorkspaceRetentionOverride
)

const (
	OperationArchive = sdkmodel.OperationArchive
	OperationPurge   = sdkmodel.OperationPurge
	OperationErase   = sdkmodel.OperationErase

	PolicyStatusDraft     = sdkmodel.PolicyStatusDraft
	PolicyStatusPublished = sdkmodel.PolicyStatusPublished
	PolicyStatusRetired   = sdkmodel.PolicyStatusRetired

	CleanupStatusPending   = sdkmodel.CleanupStatusPending
	CleanupStatusRunning   = sdkmodel.CleanupStatusRunning
	CleanupStatusPaused    = sdkmodel.CleanupStatusPaused
	CleanupStatusSucceeded = sdkmodel.CleanupStatusSucceeded
	CleanupStatusFailed    = sdkmodel.CleanupStatusFailed

	SubjectRequestExport = sdkmodel.SubjectRequestExport
	SubjectRequestErase  = sdkmodel.SubjectRequestErase

	SubjectRequestPendingVerification = sdkmodel.SubjectRequestPendingVerification
	SubjectRequestVerified            = sdkmodel.SubjectRequestVerified
	SubjectRequestPreviewed           = sdkmodel.SubjectRequestPreviewed
	SubjectRequestApproved            = sdkmodel.SubjectRequestApproved
	SubjectRequestExecuting           = sdkmodel.SubjectRequestExecuting
	SubjectRequestSucceeded           = sdkmodel.SubjectRequestSucceeded
	SubjectRequestFailed              = sdkmodel.SubjectRequestFailed

	RetentionClassProduct    = sdkmodel.RetentionClassProduct
	RetentionClassLegalAudit = sdkmodel.RetentionClassLegalAudit
	RetentionClassTechnical  = sdkmodel.RetentionClassTechnical
	RetentionClassUserErase  = sdkmodel.RetentionClassUserErase

	SensitivityInternal  = sdkmodel.SensitivityInternal
	SensitivityPII       = sdkmodel.SensitivityPII
	SensitivitySensitive = sdkmodel.SensitivitySensitive
	SensitivityFinancial = sdkmodel.SensitivityFinancial
	SensitivitySecurity  = sdkmodel.SensitivitySecurity
	SensitivityAudit     = sdkmodel.SensitivityAudit

	BackupBehaviorStandard         = sdkmodel.BackupBehaviorStandard
	BackupBehaviorDelayedErase     = sdkmodel.BackupBehaviorDelayedErase
	BackupBehaviorComplianceLocked = sdkmodel.BackupBehaviorComplianceLocked

	EraseBehaviorDelete      = sdkmodel.EraseBehaviorDelete
	EraseBehaviorAnonymize   = sdkmodel.EraseBehaviorAnonymize
	EraseBehaviorNotEligible = sdkmodel.EraseBehaviorNotEligible
)
