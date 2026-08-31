package modulehttptransport

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
)

type lifecycleHTTPHandler struct {
	governance lifecyclesdk.Governance
	mux        *http.ServeMux
}

func (h *lifecycleHTTPHandler) register() {
	h.mux.HandleFunc("GET /operations/lifecycle/policies", h.policies)
	h.mux.HandleFunc("POST /operations/lifecycle/policies", h.publishPolicy)
	h.mux.HandleFunc("POST /operations/lifecycle/legal-holds", h.createLegalHold)
	h.mux.HandleFunc("POST /operations/lifecycle/legal-holds/{holdID}/end", h.endLegalHold)
	h.mux.HandleFunc("GET /operations/lifecycle/cleanup/preview", h.cleanupPreview)
	h.mux.HandleFunc("POST /operations/lifecycle/cleanup/jobs", h.createCleanupJob)
	h.mux.HandleFunc("GET /operations/lifecycle/metrics", h.metrics)
	h.mux.HandleFunc("GET /operations/lifecycle/archive", h.archiveEntries)
	h.mux.HandleFunc("POST /operations/lifecycle/subjects", h.createSubjectRequest)
	h.mux.HandleFunc("POST /operations/lifecycle/subjects/{requestID}/verify", h.verifySubjectRequest)
	h.mux.HandleFunc("POST /operations/lifecycle/subjects/{requestID}/preview", h.previewSubjectRequest)
	h.mux.HandleFunc("POST /operations/lifecycle/subjects/{requestID}/approve", h.approveSubjectRequest)
	h.mux.HandleFunc("POST /operations/lifecycle/subjects/{requestID}/execute", h.executeSubjectRequest)
	h.mux.HandleFunc("GET /operations/lifecycle/subjects/{requestID}/download", h.downloadSubjectExport)
	h.mux.HandleFunc("GET /operations/lifecycle/external-erasures", h.externalErasures)
	h.mux.HandleFunc("POST /operations/lifecycle/external-erasures/{erasureID}/reconcile", h.reconcileExternalErasure)
	h.mux.HandleFunc("POST /operations/lifecycle/deletions/replay", h.replayDeletions)
}

type publishPolicyRequest struct {
	Policy         lifecyclemodel.RetentionPolicy `json:"policy"`
	Revision       int64                          `json:"revision,omitempty"`
	ApprovalRef    string                         `json:"approval_ref,omitempty"`
	EstimatedRows  int64                          `json:"estimated_rows,omitempty"`
	EstimatedBytes int64                          `json:"estimated_bytes,omitempty"`
	ChangePlanRef  string                         `json:"change_plan_ref,omitempty"`
}

type createLegalHoldRequest struct {
	Owner         string    `json:"owner,omitempty"`
	ResourceType  string    `json:"resource_type,omitempty"`
	ResourceID    string    `json:"resource_id,omitempty"`
	Reason        string    `json:"reason"`
	Authority     string    `json:"authority"`
	StartsAt      time.Time `json:"starts_at"`
	ReviewAt      time.Time `json:"review_at"`
	AuditEvidence string    `json:"audit_evidence"`
}

type createCleanupJobRequest struct {
	PolicyKey string                   `json:"policy_key"`
	Operation lifecyclemodel.Operation `json:"operation"`
	DryRun    bool                     `json:"dry_run"`
	Reason    string                   `json:"reason"`
}

type createSubjectRequest struct {
	Kind        lifecyclemodel.SubjectRequestKind `json:"kind"`
	SubjectType string                            `json:"subject_type"`
	SubjectID   string                            `json:"subject_id"`
	Reason      string                            `json:"reason"`
}

func (h *lifecycleHTTPHandler) policies(w http.ResponseWriter, r *http.Request) {
	items, err := h.governance.ListPolicies(r.Context(), lifecyclePrincipal(r))
	writeResult(w, map[string]any{"items": items, "count": len(items)}, err, http.StatusOK)
}

func (h *lifecycleHTTPHandler) publishPolicy(w http.ResponseWriter, r *http.Request) {
	var input publishPolicyRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	result, err := h.governance.PublishPolicy(r.Context(), lifecyclemodel.PolicyVersion{
		Policy: input.Policy, Revision: input.Revision, ApprovalRef: input.ApprovalRef,
		EstimatedRows: input.EstimatedRows, EstimatedBytes: input.EstimatedBytes, ChangePlanRef: input.ChangePlanRef,
	}, lifecyclePrincipal(r))
	writeResult(w, result, err, http.StatusCreated)
}

func (h *lifecycleHTTPHandler) createLegalHold(w http.ResponseWriter, r *http.Request) {
	var input createLegalHoldRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	principal := lifecyclePrincipal(r)
	result, err := h.governance.CreateLegalHold(r.Context(), lifecyclemodel.LegalHold{
		WorkspaceID: principal.WorkspaceID, Owner: input.Owner, ResourceType: input.ResourceType,
		ResourceID: input.ResourceID, Reason: input.Reason, Authority: input.Authority,
		StartsAt: input.StartsAt, ReviewAt: input.ReviewAt, AuditEvidence: input.AuditEvidence,
	}, principal)
	writeResult(w, result, err, http.StatusCreated)
}

func (h *lifecycleHTTPHandler) endLegalHold(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Authority string    `json:"authority"`
		Evidence  string    `json:"evidence"`
		EndedAt   time.Time `json:"ended_at"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	principal := lifecyclePrincipal(r)
	result, err := h.governance.EndLegalHold(r.Context(), principal.WorkspaceID, strings.TrimSpace(r.PathValue("holdID")), input.Authority, input.Evidence, input.EndedAt, principal)
	writeResult(w, result, err, http.StatusOK)
}

func (h *lifecycleHTTPHandler) cleanupPreview(w http.ResponseWriter, r *http.Request) {
	principal := lifecyclePrincipal(r)
	result, err := h.governance.PreviewCleanup(r.Context(), principal.WorkspaceID, strings.TrimSpace(r.URL.Query().Get("policy_key")), principal, time.Now().UTC())
	writeResult(w, result, err, http.StatusOK)
}

func (h *lifecycleHTTPHandler) createCleanupJob(w http.ResponseWriter, r *http.Request) {
	var input createCleanupJobRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	principal := lifecyclePrincipal(r)
	result, err := h.governance.CreateCleanupJob(r.Context(), lifecyclemodel.CleanupJob{
		WorkspaceID: principal.WorkspaceID, PolicyKey: input.PolicyKey,
		Operation: input.Operation, DryRun: input.DryRun, Reason: input.Reason,
	}, principal)
	writeResult(w, result, err, http.StatusAccepted)
}

func (h *lifecycleHTTPHandler) metrics(w http.ResponseWriter, r *http.Request) {
	result, err := h.governance.Metrics(r.Context(), lifecyclePrincipal(r), time.Now().UTC())
	writeResult(w, result, err, http.StatusOK)
}

func (h *lifecycleHTTPHandler) archiveEntries(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	result, err := h.governance.ListArchiveEntries(r.Context(), strings.TrimSpace(r.URL.Query().Get("source_table")), limit, lifecyclePrincipal(r))
	writeResult(w, result, err, http.StatusOK)
}

func (h *lifecycleHTTPHandler) createSubjectRequest(w http.ResponseWriter, r *http.Request) {
	var input createSubjectRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	principal := lifecyclePrincipal(r)
	result, err := h.governance.CreateSubjectRequest(r.Context(), lifecyclemodel.SubjectRequest{
		WorkspaceID: principal.WorkspaceID, Kind: input.Kind, SubjectType: input.SubjectType,
		SubjectID: input.SubjectID, Reason: input.Reason,
	}, principal)
	writeSubject(w, result, err, http.StatusAccepted)
}

func (h *lifecycleHTTPHandler) verifySubjectRequest(w http.ResponseWriter, r *http.Request) {
	var input struct {
		SecondFactorRef string `json:"second_factor_ref"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	principal := lifecyclePrincipal(r)
	result, err := h.governance.VerifySubjectRequest(r.Context(), principal.WorkspaceID, strings.TrimSpace(r.PathValue("requestID")), input.SecondFactorRef, principal)
	writeSubject(w, result, err, http.StatusOK)
}

func (h *lifecycleHTTPHandler) previewSubjectRequest(w http.ResponseWriter, r *http.Request) {
	principal := lifecyclePrincipal(r)
	result, err := h.governance.PreviewSubjectRequest(r.Context(), principal.WorkspaceID, strings.TrimSpace(r.PathValue("requestID")), principal)
	writeSubject(w, result, err, http.StatusOK)
}

func (h *lifecycleHTTPHandler) approveSubjectRequest(w http.ResponseWriter, r *http.Request) {
	principal := lifecyclePrincipal(r)
	result, err := h.governance.ApproveSubjectRequest(r.Context(), principal.WorkspaceID, strings.TrimSpace(r.PathValue("requestID")), principal)
	writeSubject(w, result, err, http.StatusOK)
}

func (h *lifecycleHTTPHandler) executeSubjectRequest(w http.ResponseWriter, r *http.Request) {
	principal := lifecyclePrincipal(r)
	result, err := h.governance.ExecuteSubjectRequest(r.Context(), principal.WorkspaceID, strings.TrimSpace(r.PathValue("requestID")), principal)
	writeSubject(w, result, err, http.StatusOK)
}

func (h *lifecycleHTTPHandler) downloadSubjectExport(w http.ResponseWriter, r *http.Request) {
	principal := lifecyclePrincipal(r)
	result, err := h.governance.DownloadSubjectExport(r.Context(), principal.WorkspaceID, strings.TrimSpace(r.PathValue("requestID")), principal, time.Now().UTC())
	writeResult(w, map[string]any{"data": result}, err, http.StatusOK)
}

func (h *lifecycleHTTPHandler) externalErasures(w http.ResponseWriter, r *http.Request) {
	result, err := h.governance.ListExternalErasures(r.Context(), strings.TrimSpace(r.URL.Query().Get("request_id")), lifecyclePrincipal(r))
	writeResult(w, map[string]any{"items": result, "count": len(result)}, err, http.StatusOK)
}

func (h *lifecycleHTTPHandler) reconcileExternalErasure(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Evidence string `json:"evidence"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	result, err := h.governance.ReconcileExternalErasure(r.Context(), strings.TrimSpace(r.PathValue("erasureID")), input.Evidence, lifecyclePrincipal(r), time.Now().UTC())
	writeResult(w, result, err, http.StatusOK)
}

func (h *lifecycleHTTPHandler) replayDeletions(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	principal := lifecyclePrincipal(r)
	count, err := h.governance.ReplayRegisteredDeletions(r.Context(), principal.WorkspaceID, limit, principal)
	writeResult(w, map[string]any{"replayed": count}, err, http.StatusOK)
}

func lifecyclePrincipal(r *http.Request) lifecycleaccess.Principal {
	principal, known := identitysdk.PrincipalFromContext(r.Context())
	permissions := make(map[string]struct{}, 3)
	for _, permission := range []string{lifecyclesdk.PermissionPolicyManage, lifecyclesdk.PermissionCleanupRun, lifecyclesdk.PermissionSubjectManage} {
		if principal.HasPermission(permission) {
			permissions[permission] = struct{}{}
		}
	}
	return lifecycleaccess.Principal{UserID: principal.UserID, WorkspaceID: principal.WorkspaceID, Known: known && principal.Known, Permissions: permissions}
}

func writeSubject(w http.ResponseWriter, result lifecyclemodel.SubjectRequest, err error, status int) {
	if err != nil {
		writeError(w, err)
		return
	}
	result.SubjectID, result.ResolvedIdentity, result.SecondFactorRef, result.ResultReference = "", "", "", ""
	result.ImpactPreview = nil
	writeJSON(w, status, result)
}

func writeResult(w http.ResponseWriter, result any, err error, status int) {
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, status, result)
}

func writeError(w http.ResponseWriter, err error) {
	status, code, retryable := http.StatusInternalServerError, "lifecycle.internal", false
	var sdkError *lifecyclesdk.Error
	if errors.As(err, &sdkError) {
		if sdkError.StatusCode != 0 {
			status = sdkError.StatusCode
		}
		if strings.TrimSpace(sdkError.Code) != "" {
			code = strings.TrimSpace(sdkError.Code)
		}
		retryable = sdkError.Retryable
	}
	writeJSON(w, status, map[string]any{"code": code, "retryable": retryable})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, value any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": "lifecycle.request.invalid_json"})
		return false
	}
	return true
}
