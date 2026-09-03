package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
	lifecyclemodel "github.com/domainry/domainry-lifecycle/internal/domain/lifecycle/model"
)

func (s *LifecycleApplicationService) Metrics(ctx context.Context, principal lifecycleaccess.Principal, now time.Time) (lifecyclemodel.Metrics, error) {
	filter, err := lifecycleDataScope(ctx, principal, lifecyclesdk.ActionLifecycleMetricsRead)
	if err != nil {
		return lifecyclemodel.Metrics{}, err
	}
	return s.evidence.Metrics(ctx, principal.WorkspaceID, now, filter)
}

func (s *LifecycleApplicationService) HealthForSystem(ctx context.Context, scope lifecycleaccess.SystemScope, now time.Time) (map[string]any, error) {
	if _, err := lifecycleaccess.NewSystemQueryScope(scope); err != nil {
		return nil, err
	}
	metrics, err := s.evidence.GlobalMetrics(ctx, scope, now)
	if err != nil {
		return nil, err
	}
	return map[string]any{"warning": metrics.Warning, "eligible_backlog": metrics.EligibleBacklog, "oldest_eligible": metrics.OldestEligible, "purged_total": metrics.PurgedTotal, "failure_total": metrics.FailureTotal, "legal_hold_count": metrics.LegalHoldCount}, nil
}

func (s *LifecycleApplicationService) ListArchiveEntries(ctx context.Context, sourceTable string, limit int, principal lifecycleaccess.Principal) ([]lifecyclemodel.ArchiveEntry, error) {
	filter, err := lifecycleDataScope(ctx, principal, lifecyclesdk.ActionLifecycleArchiveList)
	if err != nil {
		return nil, err
	}
	entries, err := s.evidence.ListArchiveEntries(ctx, principal.WorkspaceID, sourceTable, limit, filter)
	if err == nil {
		err = s.audit(ctx, principal.WorkspaceID, "lifecycle.archive.listed", principal.UserID, sourceTable, "", map[string]any{"count": len(entries)})
	}
	return entries, err
}

func (s *LifecycleApplicationService) ListExternalErasures(ctx context.Context, requestID string, principal lifecycleaccess.Principal) ([]lifecyclemodel.ExternalErasure, error) {
	filter, err := lifecycleDataScope(ctx, principal, lifecyclesdk.ActionLifecycleExternalErasuresList)
	if err != nil {
		return nil, err
	}
	items, err := s.subjectRequests.ListExternalErasures(ctx, principal.WorkspaceID, requestID, filter)
	for index := range items {
		items[index] = sanitizedExternalErasure(items[index])
	}
	return items, err
}

func (s *LifecycleApplicationService) ReconcileExternalErasure(ctx context.Context, id, evidence string, principal lifecycleaccess.Principal, now time.Time) (lifecyclemodel.ExternalErasure, error) {
	filter, err := lifecycleDataScope(ctx, principal, lifecyclesdk.ActionLifecycleExternalErasuresReconcile)
	if err != nil {
		return lifecyclemodel.ExternalErasure{}, err
	}
	if evidence == "" {
		return lifecyclemodel.ExternalErasure{}, fmt.Errorf("provider reconciliation evidence is required")
	}
	var item lifecyclemodel.ExternalErasure
	err = s.withinTransaction(ctx, func(transactionContext context.Context) error {
		var found bool
		var reconcileErr error
		item, found, reconcileErr = s.subjectRequests.ReconcileExternalErasure(transactionContext, principal.WorkspaceID, id, evidence, now, filter)
		if reconcileErr != nil {
			return reconcileErr
		}
		if !found {
			return fmt.Errorf("external erasure not found")
		}
		return s.audit(transactionContext, principal.WorkspaceID, "lifecycle.external_erasure.reconciled", principal.UserID, item.ID, "", item)
	})
	if err != nil {
		return lifecyclemodel.ExternalErasure{}, err
	}
	return sanitizedExternalErasure(item), nil
}

func sanitizedExternalErasure(item lifecyclemodel.ExternalErasure) lifecyclemodel.ExternalErasure {
	item.ProviderRef, item.Evidence = "redacted", ""
	return item
}

func (s *LifecycleApplicationService) DownloadSubjectExport(ctx context.Context, workspaceID, requestID string, principal lifecycleaccess.Principal, now time.Time) (json.RawMessage, error) {
	filter, err := lifecycleWorkspaceDataScope(ctx, principal, workspaceID, lifecyclesdk.ActionLifecycleSubjectExportsDownload)
	if err != nil {
		return nil, err
	}
	request, err := s.subjectRequest(ctx, workspaceID, requestID, filter)
	if err != nil {
		return nil, err
	}
	if request.Kind != lifecyclemodel.SubjectRequestExport || request.Status != lifecyclemodel.SubjectRequestSucceeded || request.DownloadExpiresAt.IsZero() || !now.Before(request.DownloadExpiresAt) || s.artifacts == nil {
		return nil, fmt.Errorf("subject export unavailable or expired")
	}
	payload, err := s.artifacts.ReadSubjectExport(ctx, workspaceID, request.ResultReference, now)
	if err != nil {
		return nil, err
	}
	return payload, s.audit(ctx, workspaceID, "lifecycle.subject.export_downloaded", principal.UserID, request.ID, "", map[string]any{"request_id": request.ID})
}
