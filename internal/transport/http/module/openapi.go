package modulehttptransport

import "strings"

func lifecycleOpenAPIOperations() map[string]map[string]any {
	operations := map[string]map[string]any{}
	add := func(pattern, operationID, summary string, requestBody bool) {
		operation := map[string]any{
			"operationId": operationID,
			"tags":        []string{"Lifecycle"},
			"summary":     summary,
			"security":    []map[string]any{{"BearerAuth": []string{}}},
			"responses": map[string]any{
				"200": map[string]any{"description": "Lifecycle response"},
				"400": map[string]any{"description": "Invalid lifecycle request"},
				"403": map[string]any{"description": "Lifecycle permission denied"},
			},
		}
		if strings.HasPrefix(pattern, "POST ") && requestBody {
			operation["requestBody"] = map[string]any{
				"required": true,
				"content":  map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object"}}},
			}
		}
		operations[pattern] = operation
	}
	add("GET /operations/lifecycle/policies", "listLifecyclePolicies", "List retention policy versions", false)
	add("POST /operations/lifecycle/policies", "publishLifecyclePolicy", "Publish a retention policy version", true)
	add("POST /operations/lifecycle/legal-holds", "createLifecycleLegalHold", "Create a legal hold", true)
	add("POST /operations/lifecycle/legal-holds/{holdID}/end", "endLifecycleLegalHold", "End a legal hold with evidence", true)
	add("GET /operations/lifecycle/cleanup/preview", "previewLifecycleCleanup", "Preview retention cleanup impact", false)
	add("POST /operations/lifecycle/cleanup/jobs", "createLifecycleCleanupJob", "Create a retention cleanup job", true)
	add("GET /operations/lifecycle/metrics", "getLifecycleMetrics", "Get lifecycle backlog metrics", false)
	add("GET /operations/lifecycle/archive", "listLifecycleArchive", "List sanitized archive evidence", false)
	add("POST /operations/lifecycle/subjects", "createLifecycleSubjectRequest", "Create a governed subject request", true)
	add("POST /operations/lifecycle/subjects/{requestID}/verify", "verifyLifecycleSubjectRequest", "Verify a subject request", true)
	add("POST /operations/lifecycle/subjects/{requestID}/preview", "previewLifecycleSubjectRequest", "Preview subject request impact", false)
	add("POST /operations/lifecycle/subjects/{requestID}/approve", "approveLifecycleSubjectRequest", "Approve a subject request", false)
	add("POST /operations/lifecycle/subjects/{requestID}/execute", "executeLifecycleSubjectRequest", "Execute a subject request", false)
	add("GET /operations/lifecycle/subjects/{requestID}/download", "downloadLifecycleSubjectExport", "Download an authorized subject export", false)
	add("GET /operations/lifecycle/external-erasures", "listLifecycleExternalErasures", "List external erasure reconciliation state", false)
	add("POST /operations/lifecycle/external-erasures/{erasureID}/reconcile", "reconcileLifecycleExternalErasure", "Reconcile external erasure evidence", true)
	add("POST /operations/lifecycle/deletions/replay", "replayLifecycleDeletions", "Replay registered deletions after restore", false)
	return operations
}
