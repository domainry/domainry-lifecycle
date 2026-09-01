package modulehttptransport

import (
	"time"

	"github.com/domainry/domainry-foundation/modulecapability"
)

func lifecycleOpenAPIOperations() map[string]map[string]any {
	operations := map[string]map[string]any{}
	add := func(pattern, operationID, summary, success string, body any, parameters ...map[string]any) {
		operation := map[string]any{
			"operationId": operationID,
			"tags":        []string{"Lifecycle"},
			"summary":     summary,
			"security":    []map[string]any{{"BearerAuth": []string{}}},
			"responses": map[string]any{
				success: map[string]any{"description": "Lifecycle response"},
				"400":   map[string]any{"description": "Invalid lifecycle request"},
				"403":   map[string]any{"description": "Lifecycle permission denied"},
			},
		}
		if body != nil {
			operation["requestBody"] = map[string]any{
				"required": true,
				"content": map[string]any{"application/json": map[string]any{
					"schema": modulecapability.JSONSchemaForGoValue(body),
				}},
			}
		}
		if len(parameters) != 0 {
			values := make([]any, len(parameters))
			for index := range parameters {
				values[index] = parameters[index]
			}
			operation["parameters"] = values
		}
		operations[pattern] = operation
	}
	path := func(name string) map[string]any {
		return map[string]any{"name": name, "in": "path", "required": true, "schema": map[string]any{"type": "string", "minLength": 1}}
	}
	query := func(name string, required bool, schema map[string]any) map[string]any {
		return map[string]any{"name": name, "in": "query", "required": required, "schema": schema}
	}
	add("GET /operations/lifecycle/policies", "listLifecyclePolicies", "List retention policy versions", "200", nil)
	add("POST /operations/lifecycle/policies", "publishLifecyclePolicy", "Publish a retention policy version", "201", publishPolicyRequest{})
	add("POST /operations/lifecycle/legal-holds", "createLifecycleLegalHold", "Create a legal hold", "201", createLegalHoldRequest{})
	add("POST /operations/lifecycle/legal-holds/{holdID}/end", "endLifecycleLegalHold", "End a legal hold with evidence", "200", struct {
		Authority string    `json:"authority"`
		Evidence  string    `json:"evidence"`
		EndedAt   time.Time `json:"ended_at"`
	}{}, path("holdID"))
	add("GET /operations/lifecycle/cleanup/preview", "previewLifecycleCleanup", "Preview retention cleanup impact", "200", nil, query("policy_key", true, map[string]any{"type": "string", "minLength": 1}))
	add("POST /operations/lifecycle/cleanup/jobs", "createLifecycleCleanupJob", "Create a retention cleanup job", "202", createCleanupJobRequest{})
	add("GET /operations/lifecycle/metrics", "getLifecycleMetrics", "Get lifecycle backlog metrics", "200", nil)
	add("GET /operations/lifecycle/archive", "listLifecycleArchive", "List sanitized archive evidence", "200", nil,
		query("source_table", false, map[string]any{"type": "string"}), query("limit", false, map[string]any{"type": "integer", "minimum": 1}))
	add("POST /operations/lifecycle/subjects", "createLifecycleSubjectRequest", "Create a governed subject request", "202", createSubjectRequest{})
	add("POST /operations/lifecycle/subjects/{requestID}/verify", "verifyLifecycleSubjectRequest", "Verify a subject request", "200", struct {
		SecondFactorRef string `json:"second_factor_ref"`
	}{}, path("requestID"))
	add("POST /operations/lifecycle/subjects/{requestID}/preview", "previewLifecycleSubjectRequest", "Preview subject request impact", "200", nil, path("requestID"))
	add("POST /operations/lifecycle/subjects/{requestID}/approve", "approveLifecycleSubjectRequest", "Approve a subject request", "200", nil, path("requestID"))
	add("POST /operations/lifecycle/subjects/{requestID}/execute", "executeLifecycleSubjectRequest", "Execute a subject request", "200", nil, path("requestID"))
	add("GET /operations/lifecycle/subjects/{requestID}/download", "downloadLifecycleSubjectExport", "Download an authorized subject export", "200", nil, path("requestID"))
	add("GET /operations/lifecycle/external-erasures", "listLifecycleExternalErasures", "List external erasure reconciliation state", "200", nil, query("request_id", false, map[string]any{"type": "string", "minLength": 1}))
	add("POST /operations/lifecycle/external-erasures/{erasureID}/reconcile", "reconcileLifecycleExternalErasure", "Reconcile external erasure evidence", "200", struct {
		Evidence string `json:"evidence"`
	}{}, path("erasureID"))
	add("POST /operations/lifecycle/deletions/replay", "replayLifecycleDeletions", "Replay registered deletions after restore", "200", nil, query("limit", false, map[string]any{"type": "integer", "minimum": 1}))
	return operations
}
