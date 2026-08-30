package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
	ormbuilder "github.com/domainry/domainry-orm/builder"
)

func (s LifecycleStore) GlobalMetrics(ctx context.Context, scope lifecycleaccess.SystemScope, now time.Time) (lifecyclemodel.Metrics, error) {
	if _, err := lifecycleaccess.NewSystemQueryScope(scope); err != nil {
		return lifecyclemodel.Metrics{}, err
	}
	metrics := lifecyclemodel.Metrics{}
	query, args, buildErr := ormbuilder.NewSelectBuilder(s.renderer, "lifecycle_cleanup_jobs").Projections(
		ormbuilder.Project(ormbuilder.CountAll()), ormbuilder.Project(ormbuilder.Min(ormbuilder.Column("updated_at"))),
	).Where(ormbuilder.In("status", lifecyclemodel.CleanupStatusPending, lifecyclemodel.CleanupStatusPaused, lifecyclemodel.CleanupStatusFailed)).Build()
	if buildErr != nil {
		return metrics, fmt.Errorf("build lifecycle cleanup metrics query: %w", buildErr)
	}
	var oldest sql.NullString
	if err := s.database(ctx).QueryRowContext(ctx, query, args...).Scan(&metrics.EligibleBacklog, &oldest); err != nil {
		return metrics, err
	}
	if oldest.Valid {
		metrics.OldestEligible = parseLifecycleTime(oldest.String)
	}
	query, args, buildErr = ormbuilder.NewSelectBuilder(s.renderer, "lifecycle_legal_holds").Projections(ormbuilder.Project(ormbuilder.CountAll())).Where(ormbuilder.And(
		ormbuilder.LessThanOrEqual("starts_at", lifecycleTime(now)), ormbuilder.Or(ormbuilder.Equal("ends_at", ""), ormbuilder.GreaterThan("ends_at", lifecycleTime(now))),
	)).Build()
	if buildErr != nil {
		return metrics, fmt.Errorf("build lifecycle legal hold metrics query: %w", buildErr)
	}
	if err := s.database(ctx).QueryRowContext(ctx, query, args...).Scan(&metrics.LegalHoldCount); err != nil {
		return metrics, err
	}
	query, args, buildErr = ormbuilder.NewSelectBuilder(s.renderer, "lifecycle_audit_evidence").Columns("event", "payload_json").Where(
		ormbuilder.In("event", "lifecycle.cleanup.succeeded", "lifecycle.cleanup.failed"),
	).Build()
	if buildErr != nil {
		return metrics, fmt.Errorf("build lifecycle audit metrics query: %w", buildErr)
	}
	rows, err := s.database(ctx).QueryContext(ctx, query, args...)
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
