package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/domainry/domainry-lifecycle-sdk/modulehost"
	model "github.com/domainry/domainry-lifecycle/internal/domain/lifecycle/model"
	"github.com/domainry/domainry-orm/query"
)

func (s LifecycleStore) CheckSubjectExportAllowed(ctx context.Context, workspaceID, subjectID string) error {
	statement, args, err := query.NewWorkspaceSelectBuilder(s.renderer, "_lifecycle_subject_erasure_fences", workspaceID).Columns("request_id").Where(query.Equal("subject_id", subjectID)).Build()
	if err != nil {
		return err
	}
	var requestID string
	err = s.database(ctx).QueryRowContext(ctx, statement, args...).Scan(&requestID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("subject export blocked by erasure")
}

func (s LifecycleStore) subjectRequestsForErasure(ctx context.Context, workspaceID, subjectID string) ([]model.SubjectRequest, error) {
	statement, args, err := query.NewWorkspaceSelectBuilder(s.renderer, "_lifecycle_subject_requests", workspaceID).Columns("payload_json").
		Where(query.Or(query.Equal("resolved_identity", subjectID), query.Equal("subject_id", subjectID))).OrderBy(query.Ascending("id")).Build()
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
		for _, request := range requests {
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
		statement, args, err := query.NewWorkspaceSelectBuilder(s.renderer, "_lifecycle_subject_erasure_fences", workspaceID).Columns("request_id").Where(query.Equal("subject_id", subjectID)).Build()
		if err != nil {
			return err
		}
		var existing string
		err = s.database(txctx).QueryRowContext(txctx, statement, args...).Scan(&existing)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		statement, args, err = query.NewWorkspaceInsertBuilder(s.renderer, "_lifecycle_subject_erasure_fences", workspaceID).Columns("subject_id", "request_id").Values(subjectID, requestID).Build()
		if err != nil {
			return err
		}
		_, err = s.database(txctx).ExecContext(txctx, statement, args...)
		return err
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
				statement, args, err := query.NewWorkspaceUpdateBuilder(s.renderer, "_lifecycle_audit_evidence", workspaceID).Set("payload_json", "{}").Where(query.Equal("resource_id", request.ID)).Build()
				if err != nil {
					return err
				}
				if _, err = s.database(txctx).ExecContext(txctx, statement, args...); err != nil {
					return err
				}
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
				statement, args, err := query.NewWorkspaceDeleteBuilder(s.renderer, "_lifecycle_subject_execution_steps", workspaceID).Where(query.Equal("request_id", request.ID)).Build()
				if err != nil {
					return err
				}
				if _, err = s.database(txctx).ExecContext(txctx, statement, args...); err != nil {
					return err
				}
			}
			statement, args, err := query.NewWorkspaceUpdateBuilder(s.renderer, "_lifecycle_audit_evidence", workspaceID).Set("payload_json", "{}").Where(query.Equal("resource_id", request.ID)).Build()
			if err != nil {
				return err
			}
			if _, err = s.database(txctx).ExecContext(txctx, statement, args...); err != nil {
				return err
			}
		}
		return nil
	})
}
