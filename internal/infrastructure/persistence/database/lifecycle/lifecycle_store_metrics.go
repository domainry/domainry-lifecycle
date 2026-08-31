package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	lifecyclemodel "github.com/domainry/domainry-lifecycle/internal/domain/lifecycle/model"
	"github.com/domainry/domainry-orm/query"
)

func (s LifecycleStore) Metrics(ctx context.Context, workspaceID string, now time.Time) (lifecyclemodel.Metrics, error) {
	metrics := lifecyclemodel.Metrics{}
	queryValue, args, buildErr := query.NewWorkspaceSelectBuilder(s.renderer, "_lifecycle_cleanup_jobs", workspaceID).Projections(query.Project(query.CountAll()), query.Project(query.Min(query.Column("updated_at")))).Where(query.In("status", lifecyclemodel.CleanupStatusPending, lifecyclemodel.CleanupStatusPaused, lifecyclemodel.CleanupStatusFailed)).Build()
	if buildErr != nil {
		return metrics, buildErr
	}
	var oldest sql.NullString
	if err := s.database(ctx).QueryRowContext(ctx, queryValue, args...).Scan(&metrics.EligibleBacklog, &oldest); err != nil {
		return metrics, err
	}
	if oldest.Valid {
		metrics.OldestEligible, _ = time.Parse(time.RFC3339Nano, oldest.String)
	}
	queryValue, args, buildErr = query.NewWorkspaceSelectBuilder(s.renderer, "_lifecycle_legal_holds", workspaceID).Projections(query.Project(query.CountAll())).Where(query.And(query.LessThanOrEqual("starts_at", lifecycleTime(now)), query.Or(query.Equal("ends_at", ""), query.GreaterThan("ends_at", lifecycleTime(now))))).Build()
	if buildErr != nil {
		return metrics, buildErr
	}
	if err := s.database(ctx).QueryRowContext(ctx, queryValue, args...).Scan(&metrics.LegalHoldCount); err != nil {
		return metrics, err
	}
	queryValue, args, buildErr = query.NewWorkspaceSelectBuilder(s.renderer, "_lifecycle_audit_evidence", workspaceID).Columns("event", "payload_json").Where(query.In("event", "lifecycle.cleanup.succeeded", "lifecycle.cleanup.failed")).Build()
	if buildErr != nil {
		return metrics, buildErr
	}
	rows, err := s.database(ctx).QueryContext(ctx, queryValue, args...)
	if err != nil {
		return metrics, err
	}
	defer rows.Close()
	for rows.Next() {
		var event, payload string
		if err := rows.Scan(&event, &payload); err != nil {
			return metrics, err
		}
		if event == "lifecycle.cleanup.failed" {
			metrics.FailureTotal++
			continue
		}
		var evidence lifecyclemodel.AuditEvidence
		var job lifecyclemodel.CleanupJob
		if json.Unmarshal([]byte(payload), &evidence) == nil && json.Unmarshal(evidence.Payload, &job) == nil {
			metrics.PurgedTotal += job.Purged
		}
	}
	if err := rows.Err(); err != nil {
		return metrics, err
	}
	metrics.Warning = metrics.FailureTotal > 0 || metrics.EligibleBacklog > 1000 || (!metrics.OldestEligible.IsZero() && now.Sub(metrics.OldestEligible) > 24*time.Hour)
	return metrics, nil
}

func lifecycleTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func parseLifecycleTime(value string) time.Time {
	result, _ := time.Parse(time.RFC3339Nano, value)
	return result
}
