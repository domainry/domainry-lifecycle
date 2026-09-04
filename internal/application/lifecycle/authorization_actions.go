package lifecycle

import (
	"fmt"
	"strings"

	actioncontract "github.com/domainry/domainry-foundation/action"
	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
)

const LifecycleAuthorizationOwner = "module:lifecycle"

// AuthorizationActions is Lifecycle's complete source-owned executable
// manifest. HTTP and direct Governance SDK calls share the same Action key;
// host-only system and worker calls are explicit non-role Actions.
func AuthorizationActions() ([]actioncontract.ActionDefinition, error) {
	definitions := []actioncontract.ActionDefinition{
		lifecycleRoleAction(lifecyclesdk.ActionLifecyclePoliciesList, lifecyclesdk.CapabilityLifecycleGovernance, "Retention governance", "GET /lifecycle/policies", "List lifecycle policies", actioncontract.EffectRead, actioncontract.RiskLow, "not_applicable", "owner_read_audit_policy"),
		lifecycleRoleAction(lifecyclesdk.ActionLifecyclePoliciesPublish, lifecyclesdk.CapabilityLifecycleGovernance, "Retention governance", "POST /lifecycle/policies", "Publish lifecycle policy", actioncontract.EffectWrite, actioncontract.RiskHigh, "caller_key_required", "mutation_audit_required", actioncontract.ApprovalReason),
		lifecycleRoleAction(lifecyclesdk.ActionLifecycleLegalHoldsList, lifecyclesdk.CapabilityLifecycleGovernance, "Retention governance", "GET /lifecycle/legal-holds", "List legal holds", actioncontract.EffectRead, actioncontract.RiskLow, "not_applicable", "owner_read_audit_policy"),
		lifecycleRoleAction(lifecyclesdk.ActionLifecycleLegalHoldsCreate, lifecyclesdk.CapabilityLifecycleGovernance, "Retention governance", "POST /lifecycle/legal-holds", "Create legal hold", actioncontract.EffectWrite, actioncontract.RiskHigh, "caller_key_required", "mutation_audit_required", actioncontract.ApprovalReason),
		lifecycleRoleAction(lifecyclesdk.ActionLifecycleLegalHoldsEnd, lifecyclesdk.CapabilityLifecycleGovernance, "Retention governance", "POST /lifecycle/legal-holds/{holdID}/end", "End legal hold", actioncontract.EffectWrite, actioncontract.RiskHigh, "caller_key_required", "mutation_audit_required", actioncontract.ApprovalReason),
		lifecycleRoleAction(lifecyclesdk.ActionLifecycleCleanupPreview, lifecyclesdk.CapabilityLifecycleGovernance, "Retention governance", "GET /lifecycle/cleanup/preview", "Preview lifecycle cleanup", actioncontract.EffectRead, actioncontract.RiskLow, "not_applicable", "owner_read_audit_policy"),
		lifecycleRoleAction(lifecyclesdk.ActionLifecycleCleanupJobsCreate, lifecyclesdk.CapabilityLifecycleGovernance, "Retention governance", "POST /lifecycle/cleanup/jobs", "Create lifecycle cleanup job", actioncontract.EffectWrite, actioncontract.RiskHigh, "caller_key_required", "mutation_audit_required", actioncontract.ApprovalReason),
		lifecycleRoleAction(lifecyclesdk.ActionLifecycleMetricsRead, lifecyclesdk.CapabilityLifecycleGovernance, "Retention governance", "GET /lifecycle/metrics", "Read lifecycle metrics", actioncontract.EffectRead, actioncontract.RiskLow, "not_applicable", "owner_read_audit_policy"),
		lifecycleRoleAction(lifecyclesdk.ActionLifecycleArchiveList, lifecyclesdk.CapabilityLifecycleGovernance, "Retention governance", "GET /lifecycle/archive", "List lifecycle archive", actioncontract.EffectRead, actioncontract.RiskLow, "not_applicable", "owner_read_audit_policy"),
		lifecycleRoleAction(lifecyclesdk.ActionLifecycleSubjectRequestsCreate, lifecyclesdk.CapabilityLifecycleSubjects, "Data-subject lifecycle", "POST /lifecycle/subjects", "Create subject request", actioncontract.EffectWrite, actioncontract.RiskHigh, "caller_key_required", "mutation_audit_required", actioncontract.ApprovalReason),
		lifecycleRoleAction(lifecyclesdk.ActionLifecycleSubjectRequestsList, lifecyclesdk.CapabilityLifecycleSubjects, "Data-subject lifecycle", "GET /lifecycle/subjects", "List subject requests", actioncontract.EffectRead, actioncontract.RiskLow, "not_applicable", "owner_read_audit_policy"),
		lifecycleRoleAction(lifecyclesdk.ActionLifecycleSubjectRequestsRead, lifecyclesdk.CapabilityLifecycleSubjects, "Data-subject lifecycle", "GET /lifecycle/subjects/{requestID}", "Read subject request", actioncontract.EffectRead, actioncontract.RiskLow, "not_applicable", "owner_read_audit_policy"),
		lifecycleRoleAction(lifecyclesdk.ActionLifecycleSubjectRequestsVerify, lifecyclesdk.CapabilityLifecycleSubjects, "Data-subject lifecycle", "POST /lifecycle/subjects/{requestID}/verify", "Verify subject request", actioncontract.EffectWrite, actioncontract.RiskHigh, "caller_key_required", "mutation_audit_required", actioncontract.ApprovalReason),
		lifecycleRoleAction(lifecyclesdk.ActionLifecycleSubjectRequestsPreview, lifecyclesdk.CapabilityLifecycleSubjects, "Data-subject lifecycle", "POST /lifecycle/subjects/{requestID}/preview", "Preview subject request", actioncontract.EffectRead, actioncontract.RiskLow, "not_applicable", "owner_read_audit_policy"),
		lifecycleRoleAction(lifecyclesdk.ActionLifecycleSubjectRequestsApprove, lifecyclesdk.CapabilityLifecycleSubjects, "Data-subject lifecycle", "POST /lifecycle/subjects/{requestID}/approve", "Approve subject request", actioncontract.EffectWrite, actioncontract.RiskHigh, "caller_key_required", "mutation_audit_required", actioncontract.ApprovalConfirmation),
		lifecycleRoleAction(lifecyclesdk.ActionLifecycleSubjectRequestsExecute, lifecyclesdk.CapabilityLifecycleSubjects, "Data-subject lifecycle", "POST /lifecycle/subjects/{requestID}/execute", "Execute subject request", actioncontract.EffectWrite, actioncontract.RiskHigh, "caller_key_required", "mutation_audit_required", actioncontract.ApprovalConfirmation),
		lifecycleRoleAction(lifecyclesdk.ActionLifecycleSubjectExportsDownload, lifecyclesdk.CapabilityLifecycleSubjects, "Data-subject lifecycle", "GET /lifecycle/subjects/{requestID}/download", "Download subject export", actioncontract.EffectRead, actioncontract.RiskLow, "not_applicable", "owner_read_audit_policy"),
		lifecycleRoleAction(lifecyclesdk.ActionLifecycleExternalErasuresList, lifecyclesdk.CapabilityLifecycleSubjects, "Data-subject lifecycle", "GET /lifecycle/external-erasures", "List external erasures", actioncontract.EffectRead, actioncontract.RiskLow, "not_applicable", "owner_read_audit_policy"),
		lifecycleRoleAction(lifecyclesdk.ActionLifecycleExternalErasuresReconcile, lifecyclesdk.CapabilityLifecycleSubjects, "Data-subject lifecycle", "POST /lifecycle/external-erasures/{erasureID}/reconcile", "Reconcile external erasure", actioncontract.EffectWrite, actioncontract.RiskHigh, "caller_key_required", "mutation_audit_required", actioncontract.ApprovalConfirmation),
		lifecycleRoleAction(lifecyclesdk.ActionLifecycleDeletionsReplay, lifecyclesdk.CapabilityLifecycleSubjects, "Data-subject lifecycle", "POST /lifecycle/deletions/replay", "Replay lifecycle deletions", actioncontract.EffectWrite, actioncontract.RiskHigh, "caller_key_required", "mutation_audit_required", actioncontract.ApprovalConfirmation),
		lifecycleOperationsAction(lifecyclesdk.ActionLifecycleCleanupJobsProcess, "process", "Process cleanup job", actioncontract.EffectWrite, actioncontract.RiskHigh),
		lifecycleOperationsAction(lifecyclesdk.ActionLifecycleDefaultPoliciesInstall, "install", "Install default lifecycle policies", actioncontract.EffectWrite, actioncontract.RiskMedium),
		lifecycleOperationsAction(lifecyclesdk.ActionLifecycleHealthRead, "read", "Read lifecycle health", actioncontract.EffectRead, actioncontract.RiskLow),
		lifecycleOperationsAction(lifecyclesdk.ActionLifecycleWorkersTick, "tick", "Run lifecycle worker tick", actioncontract.EffectWrite, actioncontract.RiskHigh),
	}
	result := make([]actioncontract.ActionDefinition, 0, len(definitions))
	for _, definition := range definitions {
		normalized, err := actioncontract.NormalizeDefinition(definition)
		if err != nil {
			return nil, fmt.Errorf("normalize Lifecycle Action %q: %w", definition.Key, err)
		}
		result = append(result, normalized)
	}
	return result, nil
}

func lifecycleRoleAction(key, capabilityKey, capabilityLabel, pattern, label string, effect actioncontract.EffectClass, risk actioncontract.RiskLevel, idempotency, audit string, approvals ...actioncontract.ApprovalPolicy) actioncontract.ActionDefinition {
	method, path, _ := strings.Cut(strings.TrimSpace(pattern), " ")
	separator := strings.LastIndex(key, ".")
	return actioncontract.ActionDefinition{
		Key: key, Owner: LifecycleAuthorizationOwner, SourceKind: "module_http", CapabilityKey: capabilityKey, CapabilityLabel: capabilityLabel,
		OperationKey: key[separator+1:], OperationLabel: label, Label: label,
		Exposures:     []actioncontract.Exposure{actioncontract.ExposureManagement, actioncontract.ExposureOps},
		Authorization: actioncontract.Authorization{Strategy: actioncontract.AuthorizationAuthenticated},
		HTTP:          &actioncontract.HTTPBinding{Method: method, RouteTemplate: path},
		NonHTTP:       []actioncontract.NonHTTPBinding{{Kind: "sdk", InvocationKey: key}},
		Permission: &actioncontract.PermissionDefinition{
			Key: key, Owner: LifecycleAuthorizationOwner, ResourceKey: key[:separator], OperationKey: key[separator+1:], Label: label,
			Category: capabilityLabel, LifecycleStatus: actioncontract.LifecycleActive,
		},
		EffectClass: effect, RiskLevel: risk, ApprovalPolicies: append([]actioncontract.ApprovalPolicy(nil), approvals...),
		IdempotencyDecision: idempotency, AuditClass: audit, LifecycleStatus: actioncontract.LifecycleActive,
	}
}

func lifecycleOperationsAction(key, operationKey, label string, effect actioncontract.EffectClass, risk actioncontract.RiskLevel) actioncontract.ActionDefinition {
	idempotency := "not_applicable"
	if effect == actioncontract.EffectWrite {
		idempotency = "system_operation_identity"
	}
	return actioncontract.ActionDefinition{
		Key: key, Owner: LifecycleAuthorizationOwner, SourceKind: "module_http",
		CapabilityKey: lifecyclesdk.CapabilityLifecycleOperations, CapabilityLabel: "Lifecycle operations",
		OperationKey: operationKey, OperationLabel: label, Label: label,
		Exposures:     []actioncontract.Exposure{actioncontract.ExposureOps},
		Authorization: actioncontract.Authorization{Strategy: actioncontract.AuthorizationSigned, PolicyKey: "lifecycle.system_scope"},
		NonHTTP:       []actioncontract.NonHTTPBinding{{Kind: "sdk", InvocationKey: key}},
		EffectClass:   effect, RiskLevel: risk, IdempotencyDecision: idempotency,
		AuditClass: "lifecycle_system_operation", LifecycleStatus: actioncontract.LifecycleActive,
	}
}
