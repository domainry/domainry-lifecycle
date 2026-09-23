package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	sharedsubject "github.com/domainry/domainry-foundation/subjectlifecycle"
	"github.com/domainry/domainry-lifecycle-sdk/modulehost"
	model "github.com/domainry/domainry-lifecycle/internal/domain/lifecycle/model"
	"github.com/domainry/domainry-orm/query"
)

const (
	subjectRequestsTable         = sharedsubject.RequestTableName
	subjectExecutionStepsTable   = sharedsubject.StepTableName
	subjectErasureFenceOwner     = "lifecycle"
	subjectErasureFenceOperation = "erase_fence"
)

func subjectErasureRequestIDs(renderer modulehost.Dialect, workspaceID, subjectID string) *query.SelectBuilder {
	return query.NewWorkspaceSelectBuilder(renderer, subjectRequestsTable, workspaceID).Columns("id").Where(query.And(
		query.NotEqual("request_type", model.SubjectRequestTypeExternalErase),
		query.Equal("kind", model.SubjectRequestErase),
		query.Equal("resolved_identity", subjectID),
	))
}

func subjectErasureFenceRequests(renderer modulehost.Dialect, workspaceID, subjectID string) *query.SelectBuilder {
	return query.NewWorkspaceSelectBuilder(renderer, subjectExecutionStepsTable, workspaceID).Columns("request_id").Where(query.And(
		query.Equal("owner", subjectErasureFenceOwner),
		query.Equal("operation", subjectErasureFenceOperation),
		query.InSubquery("request_id", subjectErasureRequestIDs(renderer, workspaceID, subjectID)),
	))
}

func (s LifecycleStore) CheckSubjectExportAllowed(ctx context.Context, workspaceID, subjectID string) error {
	statement, args, err := subjectErasureFenceRequests(s.renderer, workspaceID, subjectID).Limit(1).Build()
	if err != nil {
		return err
	}
	rows, err := s.database(ctx).QueryContext(ctx, statement, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return fmt.Errorf("subject export blocked by erasure")
	}
	return rows.Err()
}

func (s LifecycleStore) subjectRequestsForErasure(ctx context.Context, workspaceID, subjectID string) ([]model.SubjectRequest, error) {
	statement, args, err := query.NewWorkspaceSelectBuilder(s.renderer, subjectRequestsTable, workspaceID).Columns("payload_json").
		Where(query.And(
			query.NotEqual("request_type", model.SubjectRequestTypeExternalErase),
			query.Or(query.Equal("resolved_identity", subjectID), query.Equal("subject_id", subjectID)),
		)).OrderBy(query.Ascending("id")).Build()
	if err != nil {
		return nil, err
	}
	rows, err := s.database(ctx).QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	requests := []model.SubjectRequest{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var request model.SubjectRequest
		if err := json.Unmarshal([]byte(raw), &request); err != nil {
			return nil, err
		}
		if request.WorkspaceID != workspaceID || request.ResolvedIdentity != "" && request.ResolvedIdentity != subjectID {
			continue
		}
		requests = append(requests, request)
	}
	return requests, rows.Err()
}

func (s LifecycleStore) BeginSubjectErasure(ctx context.Context, requestID, workspaceID, subjectID string) ([]model.SubjectExportReference, error) {
	if requestID == "" || workspaceID == "" || subjectID == "" {
		return nil, fmt.Errorf("subject erasure scope is required")
	}
	refs := []model.SubjectExportReference{}
	err := s.host.Transactions().WithinTransaction(ctx, func(txctx context.Context, _ modulehost.DBTX) error {
		requests, err := s.subjectRequestsForErasure(txctx, workspaceID, subjectID)
		if err != nil {
			return err
		}
		requestFound := false
		for _, request := range requests {
			if request.ID == requestID && request.Kind == model.SubjectRequestErase && request.ResolvedIdentity == subjectID {
				requestFound = true
			}
			if request.Kind != model.SubjectRequestExport {
				continue
			}
			if request.Status == model.SubjectRequestExecuting {
				return fmt.Errorf("subject erasure blocked by executing export")
			}
			reference := request.ResultReference
			if reference == "" {
				reference = request.ID + "-export"
			}
			refs = append(refs, model.SubjectExportReference{RequestID: request.ID, Reference: reference})
		}
		if !requestFound {
			return fmt.Errorf("subject erasure request does not own resolved identity")
		}
		statement, args, err := subjectErasureFenceRequests(s.renderer, workspaceID, subjectID).Build()
		if err != nil {
			return err
		}
		rows, err := s.database(txctx).QueryContext(txctx, statement, args...)
		if err != nil {
			return err
		}
		fenced := false
		for rows.Next() {
			var existing string
			if err = rows.Scan(&existing); err != nil {
				rows.Close()
				return err
			}
			if existing != requestID {
				rows.Close()
				return fmt.Errorf("subject erasure already fenced by another request")
			}
			fenced = true
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		if fenced {
			return nil
		}
		payload, err := json.Marshal(struct {
			SubjectID string `json:"subject_id"`
		}{SubjectID: subjectID})
		if err != nil {
			return err
		}
		return s.SaveSubjectExecutionStep(txctx, model.SubjectExecutionStep{
			WorkspaceID: workspaceID, RequestID: requestID, Owner: subjectErasureFenceOwner,
			Operation: subjectErasureFenceOperation, Payload: payload, CompletedAt: time.Now().UTC(),
		})
	})
	return refs, err
}

func (s LifecycleStore) EraseSubjectRequestData(ctx context.Context, requestID, workspaceID, subjectID string) error {
	return s.host.Transactions().WithinTransaction(ctx, func(txctx context.Context, _ modulehost.DBTX) error {
		requests, err := s.subjectRequestsForErasure(txctx, workspaceID, subjectID)
		if err != nil {
			return err
		}
		for _, request := range requests {
			if request.ID == requestID {
				continue
			}
			if request.Kind == model.SubjectRequestExport && request.Status == model.SubjectRequestExecuting {
				return fmt.Errorf("subject export became executing during erasure")
			}
			request.SubjectID = subjectID
			request.Reason = "[erased]"
			request.SecondFactorRef = ""
			request.ImpactPreview = nil
			request.LastError = ""
			request.UpdatedAt = time.Now().UTC()
			request.ResultReference = ""
			request.DownloadExpiresAt = time.Time{}
			if request.Kind == model.SubjectRequestExport {
				request.Status = model.SubjectRequestFailed
				request.LastError = "subject_erased"
			}
			if err := s.SaveSubjectRequest(txctx, request); err != nil {
				return err
			}
			if request.Kind == model.SubjectRequestExport {
				statement, args, err := query.NewWorkspaceDeleteBuilder(s.renderer, subjectExecutionStepsTable, workspaceID).Where(query.Equal("request_id", request.ID)).Build()
				if err != nil {
					return err
				}
				if _, err = s.database(txctx).ExecContext(txctx, statement, args...); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
