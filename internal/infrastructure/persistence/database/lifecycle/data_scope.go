package lifecycle

import (
	lifecyclepersistence "github.com/domainry/domainry-lifecycle/internal/domain/lifecycle/repository"
	"github.com/domainry/domainry-orm/query"
)

// dataScopePredicate translates a compiled exact-Permission filter into ORM
// predicates for one Lifecycle-owned business table. Unrestricted (`all`)
// deliberately returns nil so no data-range WHERE fragment is emitted.
func dataScopePredicate(filter lifecyclepersistence.DataScopeFilter, ownerUserColumn, ownerOrgColumn string) query.Predicate {
	filter = filter.Normalized()
	if filter.Unrestricted {
		return nil
	}
	predicates := make([]query.Predicate, 0, 2)
	if ownerUserColumn != "" && len(filter.OwnerUserIDs) > 0 {
		values := make([]any, len(filter.OwnerUserIDs))
		for index := range filter.OwnerUserIDs {
			values[index] = filter.OwnerUserIDs[index]
		}
		predicates = append(predicates, query.In(ownerUserColumn, values...))
	}
	if ownerOrgColumn != "" && len(filter.OwnerOrgIDs) > 0 {
		values := make([]any, len(filter.OwnerOrgIDs))
		for index := range filter.OwnerOrgIDs {
			values[index] = filter.OwnerOrgIDs[index]
		}
		predicates = append(predicates, query.In(ownerOrgColumn, values...))
	}
	if len(predicates) == 0 {
		return query.EqualExpressions(query.Value(1), query.Value(0))
	}
	if len(predicates) == 1 {
		return predicates[0]
	}
	return query.Or(predicates...)
}

func andPredicates(predicates ...query.Predicate) query.Predicate {
	values := make([]query.Predicate, 0, len(predicates))
	for _, predicate := range predicates {
		if predicate != nil {
			values = append(values, predicate)
		}
	}
	if len(values) == 0 {
		return nil
	}
	if len(values) == 1 {
		return values[0]
	}
	return query.And(values...)
}

func (s LifecycleStore) subjectRequestScopePredicate(workspaceID string, filter lifecyclepersistence.DataScopeFilter) query.Predicate {
	if filter.Normalized().Unrestricted {
		return nil
	}
	subquery := query.NewWorkspaceSelectBuilder(s.renderer, "_lifecycle_subject_requests", workspaceID).Columns("id")
	subquery.Where(dataScopePredicate(filter, "requested_by", "owner_org_id"))
	return query.InSubquery("request_id", subquery)
}

func (s LifecycleStore) cleanupJobScopePredicate(workspaceID string, filter lifecyclepersistence.DataScopeFilter) query.Predicate {
	return s.cleanupJobReferenceScopePredicate("job_id", workspaceID, filter)
}

func (s LifecycleStore) cleanupJobReferenceScopePredicate(column, workspaceID string, filter lifecyclepersistence.DataScopeFilter) query.Predicate {
	if filter.Normalized().Unrestricted {
		return nil
	}
	subquery := query.NewWorkspaceSelectBuilder(s.renderer, "_lifecycle_cleanup_jobs", workspaceID).Columns("id")
	subquery.Where(dataScopePredicate(filter, "requested_by", "owner_org_id"))
	return query.InSubquery(column, subquery)
}
