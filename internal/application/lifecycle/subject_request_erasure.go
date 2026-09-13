package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/domainry/domainry-lifecycle-sdk/contract"
	sdkmodel "github.com/domainry/domainry-lifecycle-sdk/model"
	model "github.com/domainry/domainry-lifecycle/internal/domain/lifecycle/model"
	repository "github.com/domainry/domainry-lifecycle/internal/domain/lifecycle/repository"
)

type subjectRequestErasure struct {
	repository repository.SubjectErasureRepository
	artifacts  contract.SubjectArtifactStore
}

type subjectRequestErasurePlan struct {
	RequestID   string                         `json:"request_id"`
	WorkspaceID string                         `json:"workspace_id"`
	SubjectID   string                         `json:"subject_id"`
	Exports     []model.SubjectExportReference `json:"exports"`
}

func NewSubjectRequestErasure(repository repository.SubjectErasureRepository, artifacts contract.SubjectArtifactStore) contract.SubjectExecutionHandler {
	return subjectRequestErasure{repository: repository, artifacts: artifacts}
}
func (subjectRequestErasure) Owner(context.Context) string { return "lifecycle" }
func (s subjectRequestErasure) PreviewSubject(context.Context, string, string) (json.RawMessage, error) {
	return json.RawMessage(`{"subject_export_cleanup":true}`), nil
}
func (s subjectRequestErasure) ExportSubjectForRequest(context.Context, string, string, string) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}
func (s subjectRequestErasure) EraseSubjectForRequest(context.Context, string, string, string, []sdkmodel.LegalHold) (json.RawMessage, error) {
	return nil, fmt.Errorf("Lifecycle erasure requires a persisted plan")
}
func (s subjectRequestErasure) PrepareSubjectErasure(ctx context.Context, requestID, workspaceID, subjectID string) (json.RawMessage, error) {
	if _, ok := s.artifacts.(contract.SubjectExportDeleter); !ok {
		return nil, fmt.Errorf("subject export deletion unavailable")
	}
	exports, err := s.repository.BeginSubjectErasure(ctx, requestID, workspaceID, subjectID)
	if err != nil {
		return nil, err
	}
	return json.Marshal(subjectRequestErasurePlan{RequestID: requestID, WorkspaceID: workspaceID, SubjectID: subjectID, Exports: exports})
}
func (s subjectRequestErasure) ErasePreparedSubject(ctx context.Context, requestID, workspaceID, subjectID string, raw json.RawMessage, holds []sdkmodel.LegalHold) (json.RawMessage, error) {
	var plan subjectRequestErasurePlan
	if json.Unmarshal(raw, &plan) != nil || requestID == "" || plan.RequestID != requestID || plan.WorkspaceID != workspaceID || plan.SubjectID != subjectID {
		return nil, fmt.Errorf("Lifecycle erasure plan scope mismatch")
	}
	if len(holds) > 0 {
		return nil, fmt.Errorf("Lifecycle erasure blocked by legal hold")
	}
	deleter, ok := s.artifacts.(contract.SubjectExportDeleter)
	if !ok {
		return nil, fmt.Errorf("subject export deletion unavailable")
	}
	if err := s.repository.EraseSubjectRequestData(ctx, requestID, workspaceID, subjectID); err != nil {
		return nil, err
	}
	for _, export := range plan.Exports {
		if err := deleter.DeleteSubjectExport(ctx, workspaceID, export.Reference); err != nil {
			return nil, err
		}
	}
	return json.Marshal(struct {
		RequestID      string `json:"request_id"`
		ExportsDeleted int    `json:"exports_deleted"`
	}{requestID, len(plan.Exports)})
}
