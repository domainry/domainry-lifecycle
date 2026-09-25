package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
	lifecyclemodel "github.com/domainry/domainry-lifecycle/internal/domain/lifecycle/model"
	"github.com/domainry/domainry-orm/query"
)

func (s LifecycleStore) GlobalMetrics(ctx context.Context, scope lifecycleaccess.SystemScope, now time.Time) (lifecyclemodel.Metrics, error) {
	if _, err := lifecycleaccess.NewSystemQueryScope(scope); err != nil {
		return lifecyclemodel.Metrics{}, err
	}
	metrics := lifecyclemodel.Metrics{}
	queryValue, args, buildErr := query.NewSelectBuilder(s.renderer, "_lifecycle_cleanup_jobs").Projections(
		query.Project(query.CountAll()), query.Project(query.Min(query.Column("updated_at"))),
	).Where(query.In("status", lifecyclemodel.CleanupStatusPending, lifecyclemodel.CleanupStatusPaused, lifecyclemodel.CleanupStatusFailed)).Build()
	if buildErr != nil {
		return metrics, fmt.Errorf("build lifecycle cleanup metrics query: %w", buildErr)
	}
	var oldest sql.NullInt64
	if err := s.database(ctx).QueryRowContext(ctx, queryValue, args...).Scan(&metrics.EligibleBacklog, &oldest); err != nil {
		return metrics, err
	}
	if oldest.Valid {
		metrics.OldestEligible = parseLifecycleTime(oldest.Int64)
	}
	queryValue, args, buildErr = query.NewSelectBuilder(s.renderer, "_lifecycle_legal_holds").Projections(query.Project(query.CountAll())).Where(query.And(
		query.LessThanOrEqual("starts_at", lifecycleTime(now)), query.Or(query.Equal("ends_at", int64(0)), query.GreaterThan("ends_at", lifecycleTime(now))),
	)).Build()
	if buildErr != nil {
		return metrics, fmt.Errorf("build lifecycle legal hold metrics query: %w", buildErr)
	}
	if err := s.database(ctx).QueryRowContext(ctx, queryValue, args...).Scan(&metrics.LegalHoldCount); err != nil {
		return metrics, err
	}
	queryValue, args, buildErr = query.NewSelectBuilder(s.renderer, "_lifecycle_cleanup_jobs").Columns("payload_json").Build()
	if buildErr != nil {
		return metrics, fmt.Errorf("build lifecycle cleanup result metrics query: %w", buildErr)
	}
	rows, err := s.database(ctx).QueryContext(ctx, queryValue, args...)
	if err != nil {
		return metrics, err
	}
	defer rows.Close()
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return metrics, err
		}
		var job lifecyclemodel.CleanupJob
		if json.Unmarshal([]byte(payload), &job) != nil {
			continue
		}
		metrics.PurgedTotal += job.Purged
		if job.Status == lifecyclemodel.CleanupStatusFailed {
			metrics.FailureTotal++
		}
	}
	if err := rows.Err(); err != nil {
		return metrics, err
	}
	metrics.Warning = metrics.FailureTotal > 0 || metrics.EligibleBacklog > 1000 || (!metrics.OldestEligible.IsZero() && now.Sub(metrics.OldestEligible) > 24*time.Hour)
	return metrics, nil
}
