package lifecycle

import (
	"context"
	"database/sql"
	"time"

	"github.com/domainry/domainry-orm/query"
)

func (e OwnerExecutor) previewSpec(ctx context.Context, workspaceID string, spec cleanupSpec, policyKey string, cutoff time.Time) (int64, time.Time, error) {
	const candidateAlias = "candidate"
	predicate := cleanupPredicate(spec, cutoff, candidateAlias)
	archivePredicate := query.And(
		query.Equal("source_table", spec.table),
		query.EqualExpressions(query.QualifiedColumn("archive", "resource_id"), query.QualifiedColumn(candidateAlias, spec.idColumn)),
		query.Equal("policy_key", policyKey),
	)
	if spec.tenantColumn != "" {
		archivePredicate = query.And(archivePredicate, query.EqualExpressions(query.QualifiedColumn("archive", "workspace_id"), query.QualifiedColumn(candidateAlias, spec.tenantColumn)))
	} else {
		archivePredicate = query.And(archivePredicate, query.Equal("workspace_id", workspaceID))
	}
	archive := query.NewSelectBuilder(e.renderer, "_lifecycle_archive_entries").Alias("archive").Columns("id").Where(archivePredicate)
	predicate = query.And(predicate, query.NotExistsSubquery(archive))
	builder := query.NewSelectBuilder(e.renderer, spec.table).Alias(candidateAlias).Projections(query.Project(query.CountAll()), query.Project(query.Min(query.Column(spec.timeColumn))))
	if spec.tenantColumn != "" {
		builder = query.NewWorkspaceSelectBuilder(e.renderer, spec.table, workspaceID).Alias(candidateAlias).Projections(query.Project(query.CountAll()), query.Project(query.Min(query.Column(spec.timeColumn))))
	}
	queryValue, args, buildErr := builder.Where(predicate).Build()
	if buildErr != nil {
		return 0, time.Time{}, buildErr
	}
	var count int64
	parsed := time.Time{}
	if spec.unixNanoTime {
		var oldest sql.NullInt64
		if err := e.database(ctx).QueryRowContext(ctx, queryValue, args...).Scan(&count, &oldest); err != nil {
			return 0, time.Time{}, err
		}
		if oldest.Valid {
			parsed = time.Unix(0, oldest.Int64).UTC()
		}
	} else {
		var oldest sql.NullString
		if err := e.database(ctx).QueryRowContext(ctx, queryValue, args...).Scan(&count, &oldest); err != nil {
			return 0, time.Time{}, err
		}
		if oldest.Valid {
			parsed, _ = time.Parse(time.RFC3339Nano, oldest.String)
		}
	}
	return count, parsed, nil
}
