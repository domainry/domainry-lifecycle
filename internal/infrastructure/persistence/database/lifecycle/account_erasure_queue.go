package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/domainry/domainry-lifecycle-sdk/access"
	"github.com/domainry/domainry-lifecycle-sdk/contract"
	"github.com/domainry/domainry-lifecycle-sdk/modulehost"
	model "github.com/domainry/domainry-lifecycle/internal/domain/lifecycle/model"
	"github.com/domainry/domainry-orm/query"
)

func (s LifecycleStore) SaveAccountErasureApproval(ctx context.Context, approval contract.AccountErasureApproval) error {
	if modulehost.ExecutorFromContext(ctx, nil) == nil {
		return fmt.Errorf("account erasure requires host Action transaction")
	}
	request, found, err := s.GetAccountErasureRequest(ctx, approval.WorkspaceID, approval.RequestID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("account erasure subject request is unavailable")
	}
	request.RequestType = model.SubjectRequestTypeAccountErasure
	request.AccountErasure = &model.AccountErasureApproval{
		WorkspaceID: approval.WorkspaceID, RequestID: approval.RequestID, SubjectID: approval.SubjectID,
		RequestedBy: approval.RequestedBy, ApprovedBy: approval.ApprovedBy, OwnerOrgID: approval.OwnerOrgID,
		ActionKey: approval.ActionKey, ApprovalID: approval.ApprovalID, BindingKey: approval.BindingKey,
		ObjectKey: approval.ObjectKey, ProfileID: approval.ProfileID,
	}
	return s.SaveSubjectRequest(ctx, request)
}

func (s LifecycleStore) GetAccountErasureRequest(ctx context.Context, workspace, idempotencyKey string) (model.SubjectRequest, bool, error) {
	statement, args, err := query.NewWorkspaceSelectBuilder(s.renderer, subjectRequestsTable, workspace).
		Columns("id", "request_id", "idempotency_key", "payload_json").
		Where(query.And(query.Equal("request_type", model.SubjectRequestTypeAccountErasure), query.Equal("idempotency_key", idempotencyKey))).Build()
	if err != nil {
		return model.SubjectRequest{}, false, err
	}
	var physicalID int64
	var requestID, payload string
	var storedIdempotencyKey sql.NullString
	if err = s.database(ctx).QueryRowContext(ctx, statement, args...).Scan(&physicalID, &requestID, &storedIdempotencyKey, &payload); errors.Is(err, sql.ErrNoRows) {
		return model.SubjectRequest{}, false, nil
	} else if err != nil {
		return model.SubjectRequest{}, false, err
	}
	var request model.SubjectRequest
	if err = json.Unmarshal([]byte(payload), &request); err != nil {
		return model.SubjectRequest{}, false, err
	}
	request.ID, request.RequestID, request.IdempotencyKey = physicalID, requestID, storedIdempotencyKey.String
	return request, true, nil
}

func (s LifecycleStore) GetAccountErasureApproval(ctx context.Context, workspace, request string) (contract.AccountErasureApproval, bool, error) {
	value, found, err := s.GetAccountErasureRequest(ctx, workspace, request)
	if err != nil {
		return contract.AccountErasureApproval{}, false, err
	}
	if !found || value.RequestType != model.SubjectRequestTypeAccountErasure || value.AccountErasure == nil {
		return contract.AccountErasureApproval{}, false, nil
	}
	stored := value.AccountErasure
	approval := contract.AccountErasureApproval{
		WorkspaceID: stored.WorkspaceID, RequestID: stored.RequestID, SubjectID: stored.SubjectID,
		RequestedBy: stored.RequestedBy, ApprovedBy: stored.ApprovedBy, OwnerOrgID: stored.OwnerOrgID,
		ActionKey: stored.ActionKey, ApprovalID: stored.ApprovalID, BindingKey: stored.BindingKey,
		ObjectKey: stored.ObjectKey, ProfileID: stored.ProfileID,
	}
	if approval.WorkspaceID != workspace || approval.RequestID != request {
		return approval, false, fmt.Errorf("account erasure approval scope mismatch")
	}
	return approval, true, nil
}

func (s LifecycleStore) ListRunnableAccountErasures(ctx context.Context, limit int, now time.Time, scope access.SystemScope) ([]model.SubjectRequest, error) {
	if !scope.Valid() {
		return nil, fmt.Errorf("account erasure worker scope required")
	}
	if limit < 1 || limit > 100 {
		return nil, fmt.Errorf("account erasure worker limit invalid")
	}
	ready := query.Or(query.EqualValue(query.QualifiedColumn("r", "status"), string(model.SubjectRequestApproved)),
		query.And(query.EqualValue(query.QualifiedColumn("r", "status"), string(model.SubjectRequestFailed)), query.LessThanValue(query.QualifiedColumn("r", "updated_at"), lifecycleTime(now.Add(-30*time.Second)))),
		query.And(query.EqualValue(query.QualifiedColumn("r", "status"), string(model.SubjectRequestExecuting)), query.LessThanValue(query.QualifiedColumn("r", "updated_at"), lifecycleTime(now.Add(-5*time.Minute)))))
	statement, args, err := query.NewSelectBuilder(s.renderer, subjectRequestsTable).Alias("r").
		Projections(query.Project(query.QualifiedColumn("r", "id")), query.Project(query.QualifiedColumn("r", "request_id")), query.Project(query.QualifiedColumn("r", "payload_json"))).
		Where(query.And(
			query.EqualValue(query.QualifiedColumn("r", "request_type"), string(model.SubjectRequestTypeAccountErasure)),
			query.EqualValue(query.QualifiedColumn("r", "kind"), string(model.SubjectRequestErase)), ready,
		)).
		OrderBy(query.AscendingExpression(query.QualifiedColumn("r", "updated_at")), query.AscendingExpression(query.QualifiedColumn("r", "id"))).Limit(limit).Build()
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
		var id int64
		var requestID, raw string
		if err = rows.Scan(&id, &requestID, &raw); err != nil {
			return nil, err
		}
		var request model.SubjectRequest
		if err = json.Unmarshal([]byte(raw), &request); err != nil {
			return nil, err
		}
		request.ID, request.RequestID = id, requestID
		if request.RequestType != model.SubjectRequestTypeAccountErasure || request.AccountErasure == nil {
			return nil, fmt.Errorf("account erasure approval payload is incomplete")
		}
		requests = append(requests, request)
	}
	return requests, rows.Err()
}
