package lifecycle

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	auditcontract "github.com/domainry/domainry-audit-sdk/contract"
	sharedartifact "github.com/domainry/domainry-foundation/artifact"
	requestcontext "github.com/domainry/domainry-foundation/requestcontext"
	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	"github.com/domainry/domainry-lifecycle-sdk/modulehost"
	lifecyclemodel "github.com/domainry/domainry-lifecycle/internal/domain/lifecycle/model"
	lifecyclepersistence "github.com/domainry/domainry-lifecycle/internal/domain/lifecycle/repository"
	metadatasdk "github.com/domainry/domainry-metadata-sdk"
	metadatamodulehost "github.com/domainry/domainry-metadata-sdk/modulehost"
	"github.com/domainry/domainry-orm/query"
)

const (
	retentionPolicyDefinitionKind   = "retention_policy"
	retentionPolicyDefinitionSchema = "domainry-lifecycle-retention-policy-v1"
	retentionPolicyDefinitionSource = "lifecycle_retention_policy"
)

type LifecycleStore struct {
	host        modulehost.Host
	db          modulehost.Database
	renderer    modulehost.Dialect
	definitions metadatasdk.DefinitionStore
	audit       auditcontract.Appender
	auditWithin auditcontract.TransactionalAppender
	artifacts   sharedartifact.ManagedStore
	content     lifecyclecontract.ArtifactContentStore
}

func NewLifecycleStore(store modulehost.Host) LifecycleStore {
	definitionHost, _ := store.(modulehost.DefinitionStoreHost)
	var definitions metadatasdk.DefinitionStore
	if definitionHost != nil {
		definitions = definitionHost.DefinitionStore()
	}
	auditHost, _ := store.(modulehost.AuditStoreHost)
	var auditAppender auditcontract.Appender
	var auditWithin auditcontract.TransactionalAppender
	if auditHost != nil {
		auditAppender = auditHost.AuditAppender()
		auditWithin = auditHost.AuditTransactionalAppender()
	}
	artifactHost, _ := store.(modulehost.ArtifactStoreHost)
	var artifacts sharedartifact.ManagedStore
	var content lifecyclecontract.ArtifactContentStore
	if artifactHost != nil {
		artifacts = artifactHost.ArtifactStore()
		content = artifactHost.ArtifactContentStore()
	}
	return LifecycleStore{host: store, db: store.Database(), renderer: store.Dialect(), definitions: definitions, audit: auditAppender, auditWithin: auditWithin, artifacts: artifacts, content: content}
}

func (s LifecycleStore) database(ctx context.Context) modulehost.DBTX {
	return modulehost.ExecutorFromContext(ctx, s.db)
}

func (s LifecycleStore) SavePolicy(ctx context.Context, version lifecyclemodel.PolicyVersion) error {
	if s.definitions == nil {
		return fmt.Errorf("lifecycle shared Definition store is unavailable")
	}
	workspaceID, policyKey, publisher := strings.TrimSpace(version.WorkspaceID), strings.TrimSpace(version.Policy.Key), strings.TrimSpace(version.PublishedBy)
	if workspaceID == "" || policyKey == "" || publisher == "" || version.Revision <= 0 {
		return fmt.Errorf("lifecycle retention policy publication is incomplete")
	}
	sharedContext := s.definitionContext(ctx)
	resourceKey := retentionPolicyDefinitionKey(workspaceID, policyKey)
	current, found, err := s.definitions.Get(sharedContext, metadatasdk.DefinitionOwnerLifecycle, retentionPolicyDefinitionKind, resourceKey)
	if err != nil {
		return fmt.Errorf("load current lifecycle retention policy definition: %w", err)
	}
	expectedVersionID := metadatasdk.DefinitionNoCurrentVersion
	if found {
		currentVersion, decodeErr := decodeRetentionPolicyDefinition(current)
		if decodeErr != nil {
			return decodeErr
		}
		if currentVersion.WorkspaceID != workspaceID || currentVersion.Policy.Key != policyKey || version.Revision != currentVersion.Revision+1 {
			return fmt.Errorf("lifecycle retention policy revision conflict")
		}
		expectedVersionID = current.CurrentVersionID
	} else if version.Revision != 1 {
		return fmt.Errorf("lifecycle retention policy initial revision must be 1")
	}
	payload, err := json.Marshal(version)
	if err != nil {
		return fmt.Errorf("encode lifecycle retention policy definition: %w", err)
	}
	_, err = s.definitions.Publish(sharedContext, metadatasdk.DefinitionPublishCommand{
		Owner: metadatasdk.DefinitionOwnerLifecycle, ResourceType: retentionPolicyDefinitionKind,
		ResourceKey: resourceKey, ExpectedCurrentVersionID: expectedVersionID,
		SchemaVersion: retentionPolicyDefinitionSchema + ":revision:" + strconv.FormatInt(version.Revision, 10),
		Name:          policyKey, Payload: payload,
		SourceKind: retentionPolicyDefinitionSource, SourceID: workspaceID, PublishedBy: publisher,
	})
	if err != nil {
		return fmt.Errorf("publish lifecycle retention policy definition: %w", err)
	}
	return nil
}

func (s LifecycleStore) LatestPolicy(ctx context.Context, workspaceID, policyKey string, filter lifecyclepersistence.DataScopeFilter) (lifecyclemodel.PolicyVersion, bool, error) {
	version, found, err := s.latestPolicyForWorkspace(ctx, workspaceID, policyKey, filter)
	if err != nil || found || workspaceID == lifecycleaccess.InstallationWorkspaceID {
		return version, found, err
	}
	return s.latestPolicyForWorkspace(ctx, lifecycleaccess.InstallationWorkspaceID, policyKey, filter)
}

func (s LifecycleStore) latestPolicyForWorkspace(ctx context.Context, workspaceID, policyKey string, filter lifecyclepersistence.DataScopeFilter) (lifecyclemodel.PolicyVersion, bool, error) {
	if s.definitions == nil {
		return lifecyclemodel.PolicyVersion{}, false, fmt.Errorf("lifecycle shared Definition store is unavailable")
	}
	workspaceID, policyKey = strings.TrimSpace(workspaceID), strings.TrimSpace(policyKey)
	definition, found, err := s.definitions.Get(s.definitionContext(ctx), metadatasdk.DefinitionOwnerLifecycle, retentionPolicyDefinitionKind, retentionPolicyDefinitionKey(workspaceID, policyKey))
	if err != nil {
		return lifecyclemodel.PolicyVersion{}, false, fmt.Errorf("load lifecycle retention policy definition: %w", err)
	}
	if !found {
		return lifecyclemodel.PolicyVersion{}, false, nil
	}
	version, err := decodeRetentionPolicyDefinition(definition)
	if err != nil {
		return lifecyclemodel.PolicyVersion{}, false, err
	}
	if version.WorkspaceID != workspaceID || version.Policy.Key != policyKey {
		return lifecyclemodel.PolicyVersion{}, false, fmt.Errorf("lifecycle retention policy definition identity mismatch")
	}
	if version.Status != lifecyclemodel.PolicyStatusPublished || !filter.Allows(version.PublishedBy, version.OwnerOrgID) {
		return lifecyclemodel.PolicyVersion{}, false, nil
	}
	return version, true, nil
}

func (s LifecycleStore) ListPolicies(ctx context.Context, workspaceID string, filter lifecyclepersistence.DataScopeFilter) ([]lifecyclemodel.PolicyVersion, error) {
	if s.definitions == nil {
		return nil, fmt.Errorf("lifecycle shared Definition store is unavailable")
	}
	workspaceID = strings.TrimSpace(workspaceID)
	definitions, err := s.definitions.List(s.definitionContext(ctx), metadatasdk.DefinitionQuery{
		Owner: metadatasdk.DefinitionOwnerLifecycle, ResourceType: retentionPolicyDefinitionKind, SourceID: workspaceID,
	})
	if err != nil {
		return nil, fmt.Errorf("list lifecycle retention policy definitions: %w", err)
	}
	versions := []lifecyclemodel.PolicyVersion{}
	for _, definition := range definitions {
		version, decodeErr := decodeRetentionPolicyDefinition(definition)
		if decodeErr != nil {
			return nil, decodeErr
		}
		if version.WorkspaceID != workspaceID {
			return nil, fmt.Errorf("lifecycle retention policy definition workspace mismatch")
		}
		if version.Status == lifecyclemodel.PolicyStatusPublished && filter.Allows(version.PublishedBy, version.OwnerOrgID) {
			versions = append(versions, version)
		}
	}
	sort.Slice(versions, func(left, right int) bool {
		if versions[left].Policy.Key == versions[right].Policy.Key {
			return versions[left].Revision > versions[right].Revision
		}
		return versions[left].Policy.Key < versions[right].Policy.Key
	})
	return versions, nil
}

func (s LifecycleStore) definitionContext(ctx context.Context) context.Context {
	if executor := modulehost.ExecutorFromContext(ctx, nil); executor != nil {
		return metadatamodulehost.WithExecutor(ctx, executor)
	}
	return ctx
}

func retentionPolicyDefinitionKey(workspaceID, policyKey string) string {
	workspaceHash := sha256.Sum256([]byte(strings.TrimSpace(workspaceID)))
	policyHash := sha256.Sum256([]byte(strings.TrimSpace(policyKey)))
	return "workspace:" + hex.EncodeToString(workspaceHash[:8]) + ":retention-policy:" + hex.EncodeToString(policyHash[:16])
}

func decodeRetentionPolicyDefinition(definition metadatasdk.Definition) (lifecyclemodel.PolicyVersion, error) {
	var version lifecyclemodel.PolicyVersion
	if err := json.Unmarshal(definition.Payload, &version); err != nil {
		return lifecyclemodel.PolicyVersion{}, fmt.Errorf("decode lifecycle retention policy definition: %w", err)
	}
	return version, nil
}

func (s LifecycleStore) SaveLegalHold(ctx context.Context, hold lifecyclemodel.LegalHold) error {
	payload, _ := json.Marshal(hold)
	ends := ""
	if hold.EndsAt != nil {
		ends = lifecycleTime(*hold.EndsAt)
	}
	queryValue, args, buildErr := query.NewWorkspaceInsertBuilder(s.renderer, "_lifecycle_legal_holds", hold.WorkspaceID).
		Columns("id", "owner", "resource_type", "resource_id", "created_by", "owner_org_id", "starts_at", "ends_at", "review_at", "payload_json").
		Values(hold.ID, hold.Owner, hold.ResourceType, hold.ResourceID, hold.CreatedBy, hold.OwnerOrgID, lifecycleTime(hold.StartsAt), ends, lifecycleTime(hold.ReviewAt), string(payload)).Build()
	if buildErr != nil {
		return fmt.Errorf("build lifecycle legal hold insert: %w", buildErr)
	}
	_, err := s.database(ctx).ExecContext(ctx, queryValue, args...)
	if err != nil {
		return fmt.Errorf("save lifecycle legal hold: %w", err)
	}
	return nil
}

func (s LifecycleStore) ListLegalHolds(ctx context.Context, workspaceID string, limit int, filter lifecyclepersistence.DataScopeFilter) ([]lifecyclemodel.LegalHold, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	builder := query.NewWorkspaceSelectBuilder(s.renderer, "_lifecycle_legal_holds", workspaceID).Columns("payload_json")
	if predicate := dataScopePredicate(filter, "created_by", "owner_org_id"); predicate != nil {
		builder.Where(predicate)
	}
	queryValue, args, buildErr := builder.OrderBy(query.Descending("starts_at"), query.Descending("id")).Limit(limit).Build()
	if buildErr != nil {
		return nil, fmt.Errorf("build lifecycle legal hold list: %w", buildErr)
	}
	rows, err := s.database(ctx).QueryContext(ctx, queryValue, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	holds := []lifecyclemodel.LegalHold{}
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var hold lifecyclemodel.LegalHold
		if err := json.Unmarshal([]byte(payload), &hold); err != nil {
			return nil, err
		}
		holds = append(holds, hold)
	}
	return holds, rows.Err()
}

func (s LifecycleStore) GetLegalHold(ctx context.Context, workspaceID, holdID string, filter lifecyclepersistence.DataScopeFilter) (lifecyclemodel.LegalHold, bool, error) {
	queryValue, args, buildErr := query.NewWorkspaceSelectBuilder(s.renderer, "_lifecycle_legal_holds", workspaceID).Columns("payload_json").Where(andPredicates(query.Equal("id", holdID), dataScopePredicate(filter, "created_by", "owner_org_id"))).Build()
	if buildErr != nil {
		return lifecyclemodel.LegalHold{}, false, fmt.Errorf("build lifecycle legal hold query: %w", buildErr)
	}
	var payload string
	if err := s.database(ctx).QueryRowContext(ctx, queryValue, args...).Scan(&payload); errors.Is(err, sql.ErrNoRows) {
		return lifecyclemodel.LegalHold{}, false, nil
	} else if err != nil {
		return lifecyclemodel.LegalHold{}, false, err
	}
	var hold lifecyclemodel.LegalHold
	if err := json.Unmarshal([]byte(payload), &hold); err != nil {
		return lifecyclemodel.LegalHold{}, false, err
	}
	return hold, true, nil
}

func (s LifecycleStore) UpdateLegalHold(ctx context.Context, hold lifecyclemodel.LegalHold, filter lifecyclepersistence.DataScopeFilter) (bool, error) {
	payload, _ := json.Marshal(hold)
	ends := ""
	if hold.EndsAt != nil {
		ends = lifecycleTime(*hold.EndsAt)
	}
	queryValue, args, buildErr := query.NewWorkspaceUpdateBuilder(s.renderer, "_lifecycle_legal_holds", hold.WorkspaceID).
		Set("owner", hold.Owner).Set("resource_type", hold.ResourceType).Set("resource_id", hold.ResourceID).
		Set("starts_at", lifecycleTime(hold.StartsAt)).Set("ends_at", ends).Set("review_at", lifecycleTime(hold.ReviewAt)).Set("payload_json", string(payload)).
		Where(andPredicates(query.Equal("id", hold.ID), dataScopePredicate(filter, "created_by", "owner_org_id"))).Build()
	if buildErr != nil {
		return false, fmt.Errorf("build lifecycle legal hold update: %w", buildErr)
	}
	result, err := s.database(ctx).ExecContext(ctx, queryValue, args...)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read updated lifecycle legal hold count: %w", err)
	}
	return changed == 1, nil
}

func (s LifecycleStore) ActiveLegalHolds(ctx context.Context, target lifecyclemodel.ResourceTarget, now time.Time) ([]lifecyclemodel.LegalHold, error) {
	nowText := lifecycleTime(now)
	queryValue, args, buildErr := query.NewWorkspaceSelectBuilder(s.renderer, "_lifecycle_legal_holds", target.WorkspaceID).Columns("payload_json").Where(query.And(
		query.LessThanOrEqual("starts_at", nowText), query.Or(query.Equal("ends_at", ""), query.GreaterThan("ends_at", nowText)),
	)).Build()
	if buildErr != nil {
		return nil, fmt.Errorf("build active lifecycle legal holds query: %w", buildErr)
	}
	rows, err := s.database(ctx).QueryContext(ctx, queryValue, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	holds := []lifecyclemodel.LegalHold{}
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var hold lifecyclemodel.LegalHold
		if err := json.Unmarshal([]byte(payload), &hold); err != nil {
			return nil, err
		}
		if (target.Owner == "" || hold.Owner == "" || hold.Owner == target.Owner) && (target.ResourceType == "" || hold.ResourceType == "" || hold.ResourceType == target.ResourceType) && (target.ResourceID == "*" || target.ResourceID == "" || hold.ResourceID == "" || hold.ResourceID == target.ResourceID) {
			holds = append(holds, hold)
		}
	}
	return holds, rows.Err()
}

func (s LifecycleStore) SaveCleanupJob(ctx context.Context, job lifecyclemodel.CleanupJob) error {
	payload, _ := json.Marshal(job)
	queryValue, args, buildErr := query.NewWorkspaceInsertBuilder(s.renderer, "_lifecycle_cleanup_jobs", job.WorkspaceID).
		Columns("id", "operation_id", "policy_key", "policy_version", "status", "requested_by", "owner_org_id", "checkpoint_value", "lease_owner", "lease_expires_at", "fencing_token", "updated_at", "payload_json").
		Values(job.ID, job.OperationID, job.PolicyKey, job.PolicyVersion, job.Status, job.RequestedBy, job.OwnerOrgID, job.Checkpoint, job.LeaseOwner, lifecycleTime(job.LeaseExpiresAt), job.FencingToken, lifecycleTime(job.UpdatedAt), string(payload)).Build()
	if buildErr != nil {
		return fmt.Errorf("build lifecycle cleanup job insert: %w", buildErr)
	}
	_, err := s.database(ctx).ExecContext(ctx, queryValue, args...)
	if err != nil {
		return fmt.Errorf("save lifecycle cleanup job: %w", err)
	}
	return nil
}

func (s LifecycleStore) GetCleanupJob(ctx context.Context, workspaceID, id string) (lifecyclemodel.CleanupJob, bool, error) {
	queryValue, args, buildErr := query.NewWorkspaceSelectBuilder(s.renderer, "_lifecycle_cleanup_jobs", workspaceID).
		Columns("operation_id", "status", "checkpoint_value", "lease_owner", "lease_expires_at", "fencing_token", "updated_at", "payload_json").Where(query.Equal("id", id)).Build()
	if buildErr != nil {
		return lifecyclemodel.CleanupJob{}, false, fmt.Errorf("build lifecycle cleanup job query: %w", buildErr)
	}
	var operationID, status, checkpoint, leaseOwner, leaseExpiresAt, updatedAt, payload string
	var fencingToken int64
	err := s.database(ctx).QueryRowContext(ctx, queryValue, args...).Scan(&operationID, &status, &checkpoint, &leaseOwner, &leaseExpiresAt, &fencingToken, &updatedAt, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return lifecyclemodel.CleanupJob{}, false, nil
	}
	if err != nil {
		return lifecyclemodel.CleanupJob{}, false, err
	}
	var job lifecyclemodel.CleanupJob
	if err := json.Unmarshal([]byte(payload), &job); err != nil {
		return lifecyclemodel.CleanupJob{}, false, err
	}
	job.OperationID, job.Status = operationID, lifecyclemodel.CleanupStatus(status)
	job.Checkpoint, job.LeaseOwner, job.LeaseExpiresAt = checkpoint, leaseOwner, parseLifecycleTime(leaseExpiresAt)
	job.FencingToken, job.UpdatedAt = fencingToken, parseLifecycleTime(updatedAt)
	return job, true, nil
}

func (s LifecycleStore) ListRunnableCleanupJobs(ctx context.Context, scope lifecycleaccess.SystemScope, limit int, now time.Time) ([]lifecyclemodel.CleanupJob, error) {
	if _, err := lifecycleaccess.NewSystemQueryScope(scope); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	queryValue, args, buildErr := query.NewSelectBuilder(s.renderer, "_lifecycle_cleanup_jobs").Columns("payload_json").Where(query.And(
		query.In("status", lifecyclemodel.CleanupStatusPending, lifecyclemodel.CleanupStatusPaused, lifecyclemodel.CleanupStatusFailed, lifecyclemodel.CleanupStatusRunning),
		query.Or(query.Equal("lease_owner", ""), query.LessThanOrEqual("lease_expires_at", lifecycleTime(now))),
	)).OrderBy(query.Ascending("updated_at")).Limit(limit).Build()
	if buildErr != nil {
		return nil, fmt.Errorf("build runnable lifecycle cleanup query: %w", buildErr)
	}
	rows, err := s.database(ctx).QueryContext(ctx, queryValue, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []lifecyclemodel.CleanupJob{}
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var job lifecyclemodel.CleanupJob
		if err := json.Unmarshal([]byte(payload), &job); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s LifecycleStore) ClaimCleanupJob(ctx context.Context, workspaceID, id, owner string, ttl time.Duration, now time.Time) (lifecyclemodel.CleanupJob, bool, error) {
	if strings.TrimSpace(owner) == "" {
		return lifecyclemodel.CleanupJob{}, false, fmt.Errorf("cleanup lease owner required")
	}
	if ttl <= 0 {
		ttl = 2 * time.Minute
	}
	builder := query.NewWorkspaceUpdateBuilder(s.renderer, "_lifecycle_cleanup_jobs", workspaceID)
	if operationID := requestcontext.OwnerExecutionID(ctx); operationID != "" {
		builder.Set("operation_id", operationID)
	}
	queryValue, args, buildErr := builder.
		Set("status", lifecyclemodel.CleanupStatusRunning).Set("lease_owner", owner).Set("lease_expires_at", lifecycleTime(now.Add(ttl))).
		SetExpression("fencing_token", query.Add(query.Column("fencing_token"), query.Value(1))).Set("updated_at", lifecycleTime(now)).
		Where(query.And(query.Equal("id", id), query.In("status", lifecyclemodel.CleanupStatusPending, lifecyclemodel.CleanupStatusPaused, lifecyclemodel.CleanupStatusFailed, lifecyclemodel.CleanupStatusRunning),
			query.Or(query.Equal("lease_owner", ""), query.LessThanOrEqual("lease_expires_at", lifecycleTime(now))))).Build()
	if buildErr != nil {
		return lifecyclemodel.CleanupJob{}, false, fmt.Errorf("build lifecycle cleanup claim: %w", buildErr)
	}
	result, err := s.database(ctx).ExecContext(ctx, queryValue, args...)
	if err != nil {
		return lifecyclemodel.CleanupJob{}, false, err
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return lifecyclemodel.CleanupJob{}, false, err
	}
	job, found, err := s.GetCleanupJob(ctx, workspaceID, id)
	if err != nil || !found {
		return job, false, err
	}
	return job, true, nil
}

func (s LifecycleStore) UpdateCleanupJob(ctx context.Context, job lifecyclemodel.CleanupJob) error {
	payload, _ := json.Marshal(job)
	queryValue, args, buildErr := query.NewWorkspaceUpdateBuilder(s.renderer, "_lifecycle_cleanup_jobs", job.WorkspaceID).
		Set("operation_id", job.OperationID).Set("status", job.Status).Set("checkpoint_value", job.Checkpoint).Set("lease_owner", job.LeaseOwner).
		Set("lease_expires_at", lifecycleTime(job.LeaseExpiresAt)).Set("updated_at", lifecycleTime(job.UpdatedAt)).Set("payload_json", string(payload)).
		Where(query.And(query.Equal("id", job.ID), query.Equal("fencing_token", job.FencingToken))).Build()
	if buildErr != nil {
		return fmt.Errorf("build lifecycle cleanup update: %w", buildErr)
	}
	result, err := s.database(ctx).ExecContext(ctx, queryValue, args...)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read updated lifecycle cleanup job count: %w", err)
	}
	if count != 1 {
		return fmt.Errorf("lifecycle cleanup lease lost")
	}
	return nil
}

func (s LifecycleStore) SaveSubjectRequest(ctx context.Context, request lifecyclemodel.SubjectRequest) error {
	if request.RequestType == "" {
		request.RequestType = lifecyclemodel.SubjectRequestTypeStandard
	}
	if request.RequestType == lifecyclemodel.SubjectRequestTypeExternalErase {
		return fmt.Errorf("external erasure rows require the typed external-erasure repository")
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return err
	}
	queryValue, args, buildErr := query.NewWorkspaceUpdateBuilder(s.renderer, "_subject_requests", request.WorkspaceID).
		Set("request_type", request.RequestType).Set("kind", request.Kind).Set("status", request.Status).Set("subject_id", request.SubjectID).Set("resolved_identity", request.ResolvedIdentity).
		Set("requested_by", request.RequestedBy).Set("owner_org_id", request.OwnerOrgID).Set("download_expires_at", lifecycleTime(request.DownloadExpiresAt)).Set("updated_at", lifecycleTime(request.UpdatedAt)).Set("payload_json", string(payload)).
		Set("backup_pending", request.BackupPending).
		Where(query.Equal("id", request.ID)).Build()
	if buildErr != nil {
		return fmt.Errorf("build lifecycle subject request update: %w", buildErr)
	}
	result, err := s.database(ctx).ExecContext(ctx, queryValue, args...)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read updated lifecycle subject request count: %w", err)
	}
	if count == 1 {
		return nil
	}
	queryValue, args, buildErr = query.NewWorkspaceInsertBuilder(s.renderer, "_subject_requests", request.WorkspaceID).
		Columns("id", "request_type", "kind", "status", "subject_id", "resolved_identity", "requested_by", "owner_org_id", "download_expires_at", "backup_pending", "updated_at", "payload_json").
		Values(request.ID, request.RequestType, request.Kind, request.Status, request.SubjectID, request.ResolvedIdentity, request.RequestedBy, request.OwnerOrgID, lifecycleTime(request.DownloadExpiresAt), request.BackupPending, lifecycleTime(request.UpdatedAt), string(payload)).Build()
	if buildErr != nil {
		return fmt.Errorf("build lifecycle subject request insert: %w", buildErr)
	}
	_, err = s.database(ctx).ExecContext(ctx, queryValue, args...)
	return err
}

func (s LifecycleStore) TransitionSubjectRequest(ctx context.Context, current, next lifecyclemodel.SubjectRequest, filter lifecyclepersistence.DataScopeFilter) error {
	if next.RequestType == "" {
		next.RequestType = current.RequestType
	}
	if next.RequestType == "" {
		next.RequestType = lifecyclemodel.SubjectRequestTypeStandard
	}
	if next.Kind == lifecyclemodel.SubjectRequestExport && next.ResolvedIdentity != "" {
		if err := s.CheckSubjectExportAllowed(ctx, next.WorkspaceID, next.ResolvedIdentity); err != nil {
			return err
		}
	}
	payload, err := json.Marshal(next)
	if err != nil {
		return err
	}
	queryValue, args, buildErr := query.NewWorkspaceUpdateBuilder(s.renderer, "_subject_requests", next.WorkspaceID).
		Set("request_type", next.RequestType).Set("kind", next.Kind).Set("status", next.Status).Set("subject_id", next.SubjectID).Set("resolved_identity", next.ResolvedIdentity).
		Set("download_expires_at", lifecycleTime(next.DownloadExpiresAt)).Set("backup_pending", next.BackupPending).Set("updated_at", lifecycleTime(next.UpdatedAt)).Set("payload_json", string(payload)).
		Where(andPredicates(query.Equal("id", next.ID), query.Equal("status", current.Status), query.Equal("updated_at", lifecycleTime(current.UpdatedAt)), dataScopePredicate(filter, "requested_by", "owner_org_id"))).Build()
	if buildErr != nil {
		return fmt.Errorf("build lifecycle subject transition: %w", buildErr)
	}
	result, err := s.database(ctx).ExecContext(ctx, queryValue, args...)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read transitioned lifecycle subject request count: %w", err)
	}
	if count != 1 {
		return fmt.Errorf("subject request transition lost")
	}
	return nil
}

func (s LifecycleStore) GetSubjectRequest(ctx context.Context, workspaceID, id string, filter lifecyclepersistence.DataScopeFilter) (lifecyclemodel.SubjectRequest, bool, error) {
	queryValue, args, buildErr := query.NewWorkspaceSelectBuilder(s.renderer, "_subject_requests", workspaceID).Columns("payload_json").Where(andPredicates(query.Equal("id", id), query.NotEqual("request_type", lifecyclemodel.SubjectRequestTypeExternalErase), dataScopePredicate(filter, "requested_by", "owner_org_id"))).Build()
	if buildErr != nil {
		return lifecyclemodel.SubjectRequest{}, false, fmt.Errorf("build lifecycle subject request query: %w", buildErr)
	}
	var payload string
	err := s.database(ctx).QueryRowContext(ctx, queryValue, args...).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return lifecyclemodel.SubjectRequest{}, false, nil
	}
	if err != nil {
		return lifecyclemodel.SubjectRequest{}, false, err
	}
	var request lifecyclemodel.SubjectRequest
	if err := json.Unmarshal([]byte(payload), &request); err != nil {
		return lifecyclemodel.SubjectRequest{}, false, err
	}
	return request, true, nil
}

func (s LifecycleStore) ListSubjectRequests(ctx context.Context, workspaceID string, limit int, filter lifecyclepersistence.DataScopeFilter) ([]lifecyclemodel.SubjectRequest, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	builder := query.NewWorkspaceSelectBuilder(s.renderer, "_subject_requests", workspaceID).Columns("payload_json")
	builder.Where(andPredicates(query.NotEqual("request_type", lifecyclemodel.SubjectRequestTypeExternalErase), dataScopePredicate(filter, "requested_by", "owner_org_id")))
	queryValue, args, buildErr := builder.OrderBy(query.Descending("updated_at")).Limit(limit).Build()
	if buildErr != nil {
		return nil, fmt.Errorf("build lifecycle subject request list: %w", buildErr)
	}
	rows, err := s.database(ctx).QueryContext(ctx, queryValue, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	requests := []lifecyclemodel.SubjectRequest{}
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var request lifecyclemodel.SubjectRequest
		if err := json.Unmarshal([]byte(payload), &request); err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}
	return requests, rows.Err()
}

func (s LifecycleStore) ExpireSubjectExportReferences(ctx context.Context, scope lifecycleaccess.SystemScope, now time.Time) ([]lifecyclemodel.SubjectRequest, error) {
	if _, err := lifecycleaccess.NewSystemCommandScope(scope); err != nil {
		return nil, err
	}
	queryValue, args, buildErr := query.NewSelectBuilder(s.renderer, "_subject_requests").Columns("payload_json").Where(query.And(
		query.Equal("kind", lifecyclemodel.SubjectRequestExport), query.Equal("status", lifecyclemodel.SubjectRequestSucceeded),
		query.NotEqual("download_expires_at", ""), query.LessThanOrEqual("download_expires_at", lifecycleTime(now)),
	)).Build()
	if buildErr != nil {
		return nil, fmt.Errorf("build expired lifecycle subject export query: %w", buildErr)
	}
	rows, err := s.database(ctx).QueryContext(ctx, queryValue, args...)
	if err != nil {
		return nil, err
	}
	requests := []lifecyclemodel.SubjectRequest{}
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			_ = rows.Close()
			return nil, err
		}
		var request lifecyclemodel.SubjectRequest
		if err := json.Unmarshal([]byte(payload), &request); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if request.ResultReference != "" {
			request.ResultReference, request.UpdatedAt = "", now
			requests = append(requests, request)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	for _, request := range requests {
		if err := s.SaveSubjectRequest(ctx, request); err != nil {
			return nil, err
		}
	}
	return requests, nil
}

func (s LifecycleStore) SaveExternalErasures(ctx context.Context, erasures []lifecyclemodel.ExternalErasure) error {
	for _, erasure := range erasures {
		parent, found, err := s.GetSubjectRequest(ctx, erasure.WorkspaceID, erasure.RequestID, lifecyclepersistence.UnrestrictedDataScopeFilter())
		if err != nil {
			return err
		}
		if !found || parent.Kind != lifecyclemodel.SubjectRequestErase {
			return fmt.Errorf("external erasure parent subject request is unavailable")
		}
		var existingPayload string
		queryValue, args, buildErr := query.NewWorkspaceSelectBuilder(s.renderer, "_subject_requests", erasure.WorkspaceID).
			Columns("payload_json").Where(andPredicates(query.Equal("id", erasure.ID), query.Equal("request_type", lifecyclemodel.SubjectRequestTypeExternalErase))).Build()
		if buildErr != nil {
			return fmt.Errorf("build external erasure lookup: %w", buildErr)
		}
		if err := s.database(ctx).QueryRowContext(ctx, queryValue, args...).Scan(&existingPayload); err == nil {
			var existing lifecyclemodel.ExternalErasure
			if decodeErr := json.Unmarshal([]byte(existingPayload), &existing); decodeErr != nil {
				return decodeErr
			}
			if existing.ID != erasure.ID || existing.WorkspaceID != erasure.WorkspaceID || existing.RequestID != erasure.RequestID || existing.ConnectorKey != erasure.ConnectorKey || existing.ProviderRef != erasure.ProviderRef {
				return fmt.Errorf("external erasure identity conflict")
			}
			continue
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		payload, marshalErr := json.Marshal(erasure)
		if marshalErr != nil {
			return marshalErr
		}
		queryValue, args, buildErr = query.NewWorkspaceInsertBuilder(s.renderer, "_subject_requests", erasure.WorkspaceID).
			Columns("id", "request_type", "kind", "status", "subject_id", "resolved_identity", "requested_by", "owner_org_id", "download_expires_at", "backup_pending", "updated_at", "payload_json").
			Values(erasure.ID, lifecyclemodel.SubjectRequestTypeExternalErase, lifecyclemodel.SubjectRequestErase, erasure.Status, erasure.RequestID, parent.ResolvedIdentity, parent.RequestedBy, parent.OwnerOrgID, "", false, lifecycleTime(parent.UpdatedAt), string(payload)).Build()
		if buildErr != nil {
			return fmt.Errorf("build external erasure insert: %w", buildErr)
		}
		if _, err := s.database(ctx).ExecContext(ctx, queryValue, args...); err != nil {
			return err
		}
	}
	return nil
}

func (s LifecycleStore) ListExternalErasures(ctx context.Context, workspaceID, requestID string, filter lifecyclepersistence.DataScopeFilter) ([]lifecyclemodel.ExternalErasure, error) {
	selectBuilder := query.NewWorkspaceSelectBuilder(s.renderer, "_subject_requests", workspaceID).Columns("payload_json")
	predicates := []query.Predicate{query.Equal("request_type", lifecyclemodel.SubjectRequestTypeExternalErase), dataScopePredicate(filter, "requested_by", "owner_org_id")}
	if strings.TrimSpace(requestID) != "" {
		predicates = append(predicates, query.Equal("subject_id", strings.TrimSpace(requestID)))
	}
	if predicate := andPredicates(predicates...); predicate != nil {
		selectBuilder.Where(predicate)
	}
	queryValue, args, buildErr := selectBuilder.OrderBy(query.Ascending("updated_at"), query.Ascending("id")).Build()
	if buildErr != nil {
		return nil, fmt.Errorf("build external erasure list: %w", buildErr)
	}
	rows, err := s.database(ctx).QueryContext(ctx, queryValue, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []lifecyclemodel.ExternalErasure{}
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var item lifecyclemodel.ExternalErasure
		if err := json.Unmarshal([]byte(payload), &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s LifecycleStore) ReconcileExternalErasure(ctx context.Context, workspaceID, id, evidence string, at time.Time, filter lifecyclepersistence.DataScopeFilter) (lifecyclemodel.ExternalErasure, bool, error) {
	scopePredicate := dataScopePredicate(filter, "requested_by", "owner_org_id")
	queryValue, args, buildErr := query.NewWorkspaceSelectBuilder(s.renderer, "_subject_requests", workspaceID).Columns("payload_json").Where(andPredicates(query.Equal("id", id), query.Equal("request_type", lifecyclemodel.SubjectRequestTypeExternalErase), scopePredicate)).Build()
	if buildErr != nil {
		return lifecyclemodel.ExternalErasure{}, false, fmt.Errorf("build external erasure query: %w", buildErr)
	}
	var payload string
	if err := s.database(ctx).QueryRowContext(ctx, queryValue, args...).Scan(&payload); errors.Is(err, sql.ErrNoRows) {
		return lifecyclemodel.ExternalErasure{}, false, nil
	} else if err != nil {
		return lifecyclemodel.ExternalErasure{}, false, err
	}
	var item lifecyclemodel.ExternalErasure
	if err := json.Unmarshal([]byte(payload), &item); err != nil {
		return lifecyclemodel.ExternalErasure{}, false, err
	}
	item.Status, item.Evidence, item.ReconciledAt = "reconciled", strings.TrimSpace(evidence), at
	updated, _ := json.Marshal(item)
	queryValue, args, buildErr = query.NewWorkspaceUpdateBuilder(s.renderer, "_subject_requests", workspaceID).
		Set("status", item.Status).Set("updated_at", lifecycleTime(at)).Set("payload_json", string(updated)).
		Where(andPredicates(query.Equal("id", id), query.Equal("request_type", lifecyclemodel.SubjectRequestTypeExternalErase), scopePredicate)).Build()
	if buildErr != nil {
		return lifecyclemodel.ExternalErasure{}, false, fmt.Errorf("build external erasure reconciliation: %w", buildErr)
	}
	result, err := s.database(ctx).ExecContext(ctx, queryValue, args...)
	if err != nil {
		return lifecyclemodel.ExternalErasure{}, false, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return lifecyclemodel.ExternalErasure{}, false, fmt.Errorf("read reconciled external erasure count: %w", err)
	}
	return item, changed == 1, nil
}

func (s LifecycleStore) ListPendingDeletionRegistrations(ctx context.Context, workspaceID string, limit int, filter lifecyclepersistence.DataScopeFilter) ([]lifecyclemodel.DeletionRegistration, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	queryValue, args, buildErr := query.NewWorkspaceSelectBuilder(s.renderer, "_subject_requests", workspaceID).
		Columns("payload_json").Where(andPredicates(
		query.NotEqual("request_type", lifecyclemodel.SubjectRequestTypeExternalErase), query.Equal("kind", lifecyclemodel.SubjectRequestErase),
		query.Equal("status", lifecyclemodel.SubjectRequestSucceeded), query.Equal("backup_pending", true),
		dataScopePredicate(filter, "requested_by", "owner_org_id"),
	)).
		OrderBy(query.Ascending("updated_at")).Limit(limit).Build()
	if buildErr != nil {
		return nil, fmt.Errorf("build pending lifecycle deletion registrations: %w", buildErr)
	}
	rows, err := s.database(ctx).QueryContext(ctx, queryValue, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	registrations := []lifecyclemodel.DeletionRegistration{}
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var request lifecyclemodel.SubjectRequest
		if err := json.Unmarshal([]byte(payload), &request); err != nil {
			return nil, err
		}
		registrations = append(registrations, lifecyclemodel.DeletionRegistration{
			RequestID: request.ID, WorkspaceID: request.WorkspaceID, ResolvedIdentity: request.ResolvedIdentity,
			BackupPending: request.BackupPending, Evidence: request.ResultReference, UpdatedAt: request.UpdatedAt,
		})
	}
	return registrations, rows.Err()
}

func (s LifecycleStore) ListSubjectExecutionSteps(ctx context.Context, workspaceID, requestID string) ([]lifecyclemodel.SubjectExecutionStep, error) {
	queryValue, args, buildErr := query.NewWorkspaceSelectBuilder(s.renderer, subjectExecutionStepsTable, workspaceID).
		Columns("payload_json").Where(query.Equal("request_id", requestID)).
		OrderBy(query.Ascending("owner"), query.Ascending("operation")).Build()
	if buildErr != nil {
		return nil, fmt.Errorf("build subject execution step list: %w", buildErr)
	}
	rows, err := s.database(ctx).QueryContext(ctx, queryValue, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var steps []lifecyclemodel.SubjectExecutionStep
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var step lifecyclemodel.SubjectExecutionStep
		if err := json.Unmarshal([]byte(payload), &step); err != nil {
			return nil, err
		}
		steps = append(steps, step)
	}
	return steps, rows.Err()
}

func (s LifecycleStore) SaveSubjectExecutionStep(ctx context.Context, step lifecyclemodel.SubjectExecutionStep) error {
	if strings.TrimSpace(step.WorkspaceID) == "" || strings.TrimSpace(step.RequestID) == "" || strings.TrimSpace(step.Owner) == "" || strings.TrimSpace(step.Operation) == "" || step.CompletedAt.IsZero() {
		return fmt.Errorf("subject execution step identity and completion time are required")
	}
	payload, err := json.Marshal(step)
	if err != nil {
		return err
	}
	lookup, lookupArgs, buildErr := query.NewWorkspaceSelectBuilder(s.renderer, subjectExecutionStepsTable, step.WorkspaceID).
		Columns("payload_json").Where(query.And(query.Equal("request_id", step.RequestID), query.Equal("owner", step.Owner), query.Equal("operation", step.Operation))).Build()
	if buildErr != nil {
		return fmt.Errorf("build subject execution step lookup: %w", buildErr)
	}
	var existing string
	if err := s.database(ctx).QueryRowContext(ctx, lookup, lookupArgs...).Scan(&existing); err == nil {
		var previous lifecyclemodel.SubjectExecutionStep
		if json.Unmarshal([]byte(existing), &previous) != nil || string(previous.Payload) != string(step.Payload) {
			return fmt.Errorf("subject execution step payload conflict")
		}
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	queryValue, args, buildErr := query.NewWorkspaceInsertBuilder(s.renderer, subjectExecutionStepsTable, step.WorkspaceID).
		Columns("request_id", "owner", "operation", "payload_json", "completed_at").
		Values(step.RequestID, step.Owner, step.Operation, string(payload), lifecycleTime(step.CompletedAt)).Build()
	if buildErr != nil {
		return fmt.Errorf("build subject execution step insert: %w", buildErr)
	}
	_, err = s.database(ctx).ExecContext(ctx, queryValue, args...)
	return err
}

func (s LifecycleStore) ListArchiveEntries(ctx context.Context, workspaceID, sourceTable string, limit int, filter lifecyclepersistence.DataScopeFilter) ([]lifecyclemodel.ArchiveEntry, error) {
	if s.artifacts == nil || s.content == nil {
		return nil, fmt.Errorf("lifecycle shared Artifact store or content reader is unavailable")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	filter = filter.Normalized()
	if !filter.Unrestricted && len(filter.OwnerUserIDs) == 0 && len(filter.OwnerOrgIDs) == 0 {
		return []lifecyclemodel.ArchiveEntry{}, nil
	}
	artifactQuery := sharedartifact.Query{
		Owner: sharedartifact.OwnerLifecycle, Kind: "archive", Statuses: []sharedartifact.Status{sharedartifact.StatusAvailable},
		CreatedBy: filter.OwnerUserIDs, OwnerOrgIDs: filter.OwnerOrgIDs, NewestFirst: true, Limit: limit,
	}
	if sourceTable = strings.TrimSpace(sourceTable); sourceTable != "" {
		artifactQuery.Binding = &sharedartifact.BindingQuery{Owner: sharedartifact.OwnerLifecycle, Kind: sharedartifact.BindingObjectField, ResourceType: sourceTable}
	}
	artifacts, err := s.artifacts.List(lifecycleArtifactContext(ctx), strings.TrimSpace(workspaceID), artifactQuery)
	if err != nil {
		return nil, err
	}
	entries := make([]lifecyclemodel.ArchiveEntry, 0, len(artifacts))
	for _, artifact := range artifacts {
		var metadata archiveArtifactMetadata
		if err := json.Unmarshal(artifact.Metadata, &metadata); err != nil || metadata.SourceTable == "" || metadata.ResourceID == "" || metadata.PolicyKey == "" || metadata.JobID == "" {
			return nil, fmt.Errorf("Lifecycle archive Artifact %q metadata is invalid", artifact.ID)
		}
		if sourceTable != "" && metadata.SourceTable != sourceTable {
			return nil, fmt.Errorf("Lifecycle archive Artifact %q source binding mismatch", artifact.ID)
		}
		reader, err := s.content.Open(ctx, artifact.WorkspaceID, artifact.StorageReference)
		if err != nil {
			return nil, fmt.Errorf("open Lifecycle archive Artifact %q: %w", artifact.ID, err)
		}
		payload, readErr := io.ReadAll(io.LimitReader(reader, artifact.SizeBytes+1))
		closeErr := reader.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read Lifecycle archive Artifact %q: %w", artifact.ID, readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close Lifecycle archive Artifact %q: %w", artifact.ID, closeErr)
		}
		digest := sha256.Sum256(payload)
		if int64(len(payload)) != artifact.SizeBytes || !strings.EqualFold(hex.EncodeToString(digest[:]), artifact.ContentSHA256) {
			return nil, fmt.Errorf("Lifecycle archive Artifact %q content evidence mismatch", artifact.ID)
		}
		entry := lifecyclemodel.ArchiveEntry{
			ID: artifact.ID, WorkspaceID: artifact.WorkspaceID, Owner: metadata.Owner, SourceTable: metadata.SourceTable,
			ResourceID: metadata.ResourceID, PolicyKey: metadata.PolicyKey, PolicyVersion: metadata.PolicyVersion, JobID: metadata.JobID,
			PayloadHash: artifact.ContentSHA256, Payload: json.RawMessage(payload), ArchivedAt: artifact.CreatedAt,
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func (s LifecycleStore) AppendComplianceEvent(ctx context.Context, request auditcontract.AppendRequest) error {
	if transaction := modulehost.ExecutorFromContext(ctx, nil); transaction != nil {
		if s.auditWithin == nil {
			return fmt.Errorf("lifecycle shared transactional Audit appender is unavailable")
		}
		_, err := s.auditWithin.AppendWithin(ctx, auditTransaction{DBTX: transaction}, request)
		return err
	}
	if s.audit == nil {
		return fmt.Errorf("lifecycle shared Audit appender is unavailable")
	}
	_, err := s.audit.Append(ctx, request)
	return err
}

type auditTransaction struct{ modulehost.DBTX }

func (transaction auditTransaction) ExecContext(ctx context.Context, statement string, arguments ...any) (auditcontract.Result, error) {
	return transaction.DBTX.ExecContext(ctx, statement, arguments...)
}

func (transaction auditTransaction) QueryRowContext(ctx context.Context, statement string, arguments ...any) auditcontract.Row {
	return transaction.DBTX.QueryRowContext(ctx, statement, arguments...)
}
