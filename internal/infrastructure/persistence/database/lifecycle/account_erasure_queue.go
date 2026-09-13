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
	payload, err := json.Marshal(approval)
	if err != nil {
		return err
	}
	statement, args, err := query.NewWorkspaceInsertBuilder(s.renderer, "_lifecycle_account_erasure_approvals", approval.WorkspaceID).
		Columns("request_id", "payload_json").Values(approval.RequestID, string(payload)).Build()
	if err != nil {
		return err
	}
	_, err = s.database(ctx).ExecContext(ctx, statement, args...)
	return err
}

func (s LifecycleStore) GetAccountErasureApproval(ctx context.Context, workspace, request string) (contract.AccountErasureApproval, bool, error) {
	statement, args, err := query.NewWorkspaceSelectBuilder(s.renderer, "_lifecycle_account_erasure_approvals", workspace).
		Columns("payload_json").Where(query.Equal("request_id", request)).Build()
	if err != nil {
		return contract.AccountErasureApproval{}, false, err
	}
	var raw string
	err = s.database(ctx).QueryRowContext(ctx, statement, args...).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return contract.AccountErasureApproval{}, false, nil
	}
	if err != nil {
		return contract.AccountErasureApproval{}, false, err
	}
	var approval contract.AccountErasureApproval
	if err = json.Unmarshal([]byte(raw), &approval); err != nil {
		return approval, false, err
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
	statement, args, err := query.NewSelectBuilder(s.renderer, "_lifecycle_subject_requests").Alias("r").
		Projections(query.Project(query.QualifiedColumn("r", "payload_json"))).
		Join(query.InnerJoin("_lifecycle_account_erasure_approvals", "a", query.And(
			query.EqualExpressions(query.QualifiedColumn("r", "workspace_id"), query.QualifiedColumn("a", "workspace_id")), query.EqualExpressions(query.QualifiedColumn("r", "id"), query.QualifiedColumn("a", "request_id"))))).
		Where(query.And(query.EqualValue(query.QualifiedColumn("r", "kind"), string(model.SubjectRequestErase)), ready)).
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
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var request model.SubjectRequest
		if err = json.Unmarshal([]byte(raw), &request); err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}
	return requests, rows.Err()
}
