// Package lifecyclesdk adapts Lifecycle application and infrastructure
// services to the public deployment-neutral SDK.
package lifecyclesdk

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	actioncontract "github.com/domainry/domainry-foundation/action"
	"github.com/domainry/domainry-foundation/modulehttp"
	sdk "github.com/domainry/domainry-lifecycle-sdk"
	"github.com/domainry/domainry-lifecycle-sdk/access"
	"github.com/domainry/domainry-lifecycle-sdk/contract"
	sdkmodel "github.com/domainry/domainry-lifecycle-sdk/model"
	"github.com/domainry/domainry-lifecycle-sdk/modulehost"
	lifecycleapplication "github.com/domainry/domainry-lifecycle/internal/application/lifecycle"
	internalmodel "github.com/domainry/domainry-lifecycle/internal/domain/lifecycle/model"
	infra "github.com/domainry/domainry-lifecycle/internal/infrastructure/persistence"
	modulehttptransport "github.com/domainry/domainry-lifecycle/internal/transport/http/module"
	metadatasdk "github.com/domainry/domainry-metadata-sdk"
)

type Binding struct {
	mu         sync.RWMutex
	repository infra.LifecycleStore
	host       modulehost.Host
	service    *lifecycleapplication.LifecycleApplicationService
	workers    *lifecycleapplication.WorkerRunner
	adapters   []modulehttp.Adapter
	bound      bool
}

func NewBinding(host modulehost.Host, definitions metadatasdk.DefinitionStore) (*Binding, error) {
	return &Binding{repository: infra.NewLifecycleStore(host, definitions), host: host}, nil
}

func (*Binding) Descriptor() sdk.Descriptor {
	return sdk.Descriptor{
		ProtocolVersion: sdk.ProtocolVersionV1,
		Mode:            sdk.DeploymentModeModule,
		Capabilities: sdk.Capabilities{
			Governance: true, SubjectRequests: true, RetentionWorker: true,
			AccountErasure:  true,
			UploadArtifacts: true, ArchiveInspection: true,
		},
	}
}

func (b *Binding) BindOwners(ctx context.Context, extensions sdk.OwnerExtensions) error {
	if b == nil || b.host == nil {
		return &sdk.Error{StatusCode: 503, Code: "lifecycle.binding_unavailable"}
	}
	if len(extensions.Executors) == 0 || extensions.SubjectResolver == nil || len(extensions.SubjectHandlers) == 0 || extensions.Artifacts == nil {
		return &sdk.Error{StatusCode: 500, Code: "lifecycle.owner_extensions_incomplete"}
	}
	executorOwners := map[string]bool{}
	for _, executor := range extensions.Executors {
		owner := ""
		if executor != nil {
			owner = strings.TrimSpace(executor.Owner(ctx))
		}
		if owner == "" || executorOwners[owner] {
			return &sdk.Error{StatusCode: 500, Code: "lifecycle.owner_executor_invalid"}
		}
		executorOwners[owner] = true
	}
	subjectOwners := map[string]bool{}
	subjectHandlers := make([]contract.SubjectExecutionHandler, 0, len(extensions.SubjectHandlers)+1)
	subjectOwners["lifecycle"] = true
	subjectHandlers = append(subjectHandlers, lifecycleapplication.NewSubjectRequestErasure(b.repository, extensions.Artifacts))
	for _, handler := range extensions.SubjectHandlers {
		owner := ""
		if handler != nil {
			owner = strings.TrimSpace(handler.Owner(ctx))
		}
		if owner == "" || subjectOwners[owner] {
			return &sdk.Error{StatusCode: 500, Code: "lifecycle.subject_handler_invalid"}
		}
		subjectOwners[owner] = true
		subjectHandlers = append(subjectHandlers, handler)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.bound {
		return &sdk.Error{StatusCode: 409, Code: "lifecycle.owner_extensions_already_bound"}
	}
	service := lifecycleapplication.NewLifecycleApplicationService(ctx, lifecycleapplication.LifecycleApplicationDependencies{
		Policies: b.repository, LegalHolds: b.repository, CleanupJobs: b.repository,
		SubjectRequests: b.repository, Evidence: b.repository, Compliance: b.repository,
		Executors: extensions.Executors, SubjectResolver: extensions.SubjectResolver,
		SubjectHandlers: subjectHandlers, ExternalErasure: extensions.ExternalErasure,
		Artifacts: extensions.Artifacts, UploadArtifacts: extensions.UploadArtifacts, Transactions: b.host.Transactions(),
	})
	adapter, err := modulehttptransport.NewAdapter(governanceBinding{service: service})
	if err != nil {
		return &sdk.Error{StatusCode: 500, Code: "lifecycle.http_adapter_invalid", Cause: err}
	}
	b.service = service
	b.workers = lifecycleapplication.NewWorkerRunner(service)
	b.adapters = []modulehttp.Adapter{adapter}
	b.bound = true
	return nil
}

func (b *Binding) HTTPAdapters() []modulehttp.Adapter {
	if b == nil {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return append([]modulehttp.Adapter(nil), b.adapters...)
}

func (*Binding) AuthorizationActions() ([]actioncontract.ActionDefinition, error) {
	return lifecycleapplication.AuthorizationActions()
}

func (b *Binding) Governance() sdk.Governance {
	if service := b.application(); service != nil {
		return governanceBinding{service: service}
	}
	return nil
}

func (b *Binding) System() sdk.System {
	if service := b.application(); service != nil {
		return systemBinding{service: service}
	}
	return nil
}

func (b *Binding) LocalWorkers() (sdk.LocalWorkers, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.workers == nil {
		return nil, false
	}
	return workerBinding{runner: b.workers}, true
}

func (b *Binding) application() *lifecycleapplication.LifecycleApplicationService {
	if b == nil {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.service
}

func (b *Binding) UploadArtifacts(options sdk.UploadArtifactOptions) (contract.UploadFileArtifactStore, error) {
	if b == nil || b.host == nil || (strings.TrimSpace(options.Root) == "" && options.Content == nil) || options.Fields == nil {
		return nil, &sdk.Error{StatusCode: 400, Code: "lifecycle.upload_artifact_options_invalid"}
	}
	storeOptions := make([]infra.FileArtifactStoreOption, 0, 2)
	if options.References != nil {
		storeOptions = append(storeOptions, infra.WithUploadArtifactReferences(options.References))
	}
	if options.ExpiredReferences != nil {
		storeOptions = append(storeOptions, infra.WithExpiredUploadReferenceCleaner(options.ExpiredReferences))
	}
	if options.Content != nil {
		storeOptions = append(storeOptions, infra.WithArtifactContentStore(options.Content))
	}
	return infra.NewFileArtifactStore(b.host, options.Fields, options.Root, storeOptions...), nil
}

func (b *Binding) SubjectArtifacts(root string, content ...contract.ArtifactContentStore) (contract.SubjectArtifactStore, error) {
	var storage contract.ArtifactContentStore
	if len(content) > 0 {
		storage = content[0]
	}
	if strings.TrimSpace(root) == "" && storage == nil {
		return nil, &sdk.Error{StatusCode: 400, Code: "lifecycle.subject_artifact_root_required"}
	}
	return infra.NewSubjectArtifactStore(b.host, root, storage), nil
}

func (b *Binding) ArchiveStore() contract.ArchiveStore {
	if b == nil || b.host == nil {
		return nil
	}
	return archiveStoreBinding{writer: infra.NewArchiveWriter(b.host)}
}

func (*Binding) Close(context.Context) error { return nil }

type governanceBinding struct {
	service *lifecycleapplication.LifecycleApplicationService
}

func (b governanceBinding) PublishPolicy(ctx context.Context, value sdkmodel.PolicyVersion, principal access.Principal) (sdkmodel.PolicyVersion, error) {
	input, err := convert[internalmodel.PolicyVersion](value)
	if err != nil {
		return sdkmodel.PolicyVersion{}, err
	}
	result, err := b.service.PublishPolicy(ctx, input, principal)
	return convertResult[sdkmodel.PolicyVersion](result, err)
}
func (b governanceBinding) ListPolicies(ctx context.Context, principal access.Principal) ([]sdkmodel.PolicyVersion, error) {
	result, err := b.service.ListPolicies(ctx, principal)
	return convertResult[[]sdkmodel.PolicyVersion](result, err)
}
func (b governanceBinding) ListLegalHolds(ctx context.Context, limit int, principal access.Principal) ([]sdkmodel.LegalHold, error) {
	result, err := b.service.ListLegalHolds(ctx, limit, principal)
	return convertResult[[]sdkmodel.LegalHold](result, err)
}
func (b governanceBinding) CreateLegalHold(ctx context.Context, value sdkmodel.LegalHold, principal access.Principal) (sdkmodel.LegalHold, error) {
	input, err := convert[internalmodel.LegalHold](value)
	if err != nil {
		return sdkmodel.LegalHold{}, err
	}
	result, err := b.service.CreateLegalHold(ctx, input, principal)
	return convertResult[sdkmodel.LegalHold](result, err)
}
func (b governanceBinding) EndLegalHold(ctx context.Context, workspaceID, holdID, authority, evidence string, endedAt time.Time, principal access.Principal) (sdkmodel.LegalHold, error) {
	result, err := b.service.EndLegalHold(ctx, workspaceID, holdID, authority, evidence, endedAt, principal)
	return convertResult[sdkmodel.LegalHold](result, err)
}
func (b governanceBinding) PreviewCleanup(ctx context.Context, workspaceID, policyKey string, principal access.Principal, now time.Time) (contract.CleanupPreview, error) {
	result, err := b.service.PreviewCleanup(ctx, workspaceID, policyKey, principal, now)
	return result, adaptError(err)
}
func (b governanceBinding) CreateCleanupJob(ctx context.Context, value sdkmodel.CleanupJob, principal access.Principal) (sdkmodel.CleanupJob, error) {
	input, err := convert[internalmodel.CleanupJob](value)
	if err != nil {
		return sdkmodel.CleanupJob{}, err
	}
	result, err := b.service.CreateCleanupJob(ctx, input, principal)
	return convertResult[sdkmodel.CleanupJob](result, err)
}
func (b governanceBinding) InspectCleanupJob(ctx context.Context, workspaceID, jobID string, principal access.Principal) (sdkmodel.CleanupJob, error) {
	result, err := b.service.InspectCleanupJob(ctx, workspaceID, jobID, principal)
	return convertResult[sdkmodel.CleanupJob](result, err)
}
func (b governanceBinding) ProcessCleanupJob(ctx context.Context, workspaceID, jobID, leaseOwner string, leaseTTL time.Duration, batchSize int, now time.Time, principal access.Principal) (sdkmodel.CleanupJob, error) {
	result, err := b.service.ProcessCleanupJob(ctx, workspaceID, jobID, leaseOwner, leaseTTL, batchSize, now, principal)
	return convertResult[sdkmodel.CleanupJob](result, err)
}
func (b governanceBinding) Metrics(ctx context.Context, principal access.Principal, now time.Time) (sdkmodel.Metrics, error) {
	result, err := b.service.Metrics(ctx, principal, now)
	return convertResult[sdkmodel.Metrics](result, err)
}
func (b governanceBinding) ListArchiveEntries(ctx context.Context, sourceTable string, limit int, principal access.Principal) ([]sdkmodel.ArchiveEntry, error) {
	result, err := b.service.ListArchiveEntries(ctx, sourceTable, limit, principal)
	return convertResult[[]sdkmodel.ArchiveEntry](result, err)
}
func (b governanceBinding) CreateSubjectRequest(ctx context.Context, value sdkmodel.SubjectRequest, principal access.Principal) (sdkmodel.SubjectRequest, error) {
	input, err := convert[internalmodel.SubjectRequest](value)
	if err != nil {
		return sdkmodel.SubjectRequest{}, err
	}
	result, err := b.service.CreateSubjectRequest(ctx, input, principal)
	return convertResult[sdkmodel.SubjectRequest](result, err)
}
func (b governanceBinding) ListSubjectRequests(ctx context.Context, limit int, principal access.Principal) ([]sdkmodel.SubjectRequest, error) {
	result, err := b.service.ListSubjectRequests(ctx, limit, principal)
	return convertResult[[]sdkmodel.SubjectRequest](result, err)
}
func (b governanceBinding) GetSubjectRequest(ctx context.Context, workspaceID, requestID string, principal access.Principal) (sdkmodel.SubjectRequest, error) {
	result, err := b.service.GetSubjectRequest(ctx, workspaceID, requestID, principal)
	return convertResult[sdkmodel.SubjectRequest](result, err)
}
func (b governanceBinding) VerifySubjectRequest(ctx context.Context, workspaceID, requestID, secondFactor string, principal access.Principal) (sdkmodel.SubjectRequest, error) {
	result, err := b.service.VerifySubjectRequest(ctx, workspaceID, requestID, secondFactor, principal)
	return convertResult[sdkmodel.SubjectRequest](result, err)
}
func (b governanceBinding) PreviewSubjectRequest(ctx context.Context, workspaceID, requestID string, principal access.Principal) (sdkmodel.SubjectRequest, error) {
	result, err := b.service.PreviewSubjectRequest(ctx, workspaceID, requestID, principal)
	return convertResult[sdkmodel.SubjectRequest](result, err)
}
func (b governanceBinding) ApproveSubjectRequest(ctx context.Context, workspaceID, requestID string, principal access.Principal) (sdkmodel.SubjectRequest, error) {
	result, err := b.service.ApproveSubjectRequest(ctx, workspaceID, requestID, principal)
	return convertResult[sdkmodel.SubjectRequest](result, err)
}
func (b governanceBinding) ExecuteSubjectRequest(ctx context.Context, workspaceID, requestID string, principal access.Principal) (sdkmodel.SubjectRequest, error) {
	result, err := b.service.ExecuteSubjectRequest(ctx, workspaceID, requestID, principal)
	return convertResult[sdkmodel.SubjectRequest](result, err)
}
func (b governanceBinding) DownloadSubjectExport(ctx context.Context, workspaceID, requestID string, principal access.Principal, now time.Time) (json.RawMessage, error) {
	result, err := b.service.DownloadSubjectExport(ctx, workspaceID, requestID, principal, now)
	return result, adaptError(err)
}
func (b governanceBinding) ListExternalErasures(ctx context.Context, requestID string, principal access.Principal) ([]sdkmodel.ExternalErasure, error) {
	result, err := b.service.ListExternalErasures(ctx, requestID, principal)
	return convertResult[[]sdkmodel.ExternalErasure](result, err)
}
func (b governanceBinding) ReconcileExternalErasure(ctx context.Context, id, evidence string, principal access.Principal, now time.Time) (sdkmodel.ExternalErasure, error) {
	result, err := b.service.ReconcileExternalErasure(ctx, id, evidence, principal, now)
	return convertResult[sdkmodel.ExternalErasure](result, err)
}
func (b governanceBinding) ReplayRegisteredDeletions(ctx context.Context, workspaceID string, limit int, principal access.Principal) (int, error) {
	result, err := b.service.ReplayRegisteredDeletions(ctx, workspaceID, limit, principal)
	return result, adaptError(err)
}

type systemBinding struct {
	service *lifecycleapplication.LifecycleApplicationService
}

func (b systemBinding) InstallDefaultPolicies(ctx context.Context, workspaceID string, principal access.Principal, now time.Time) error {
	return adaptError(b.service.InstallDefaultPolicies(ctx, workspaceID, principal, now))
}
func (b systemBinding) Health(ctx context.Context, scope access.SystemScope, now time.Time) (map[string]any, error) {
	result, err := b.service.HealthForSystem(ctx, scope, now)
	return result, adaptError(err)
}

type workerBinding struct {
	runner *lifecycleapplication.WorkerRunner
}

func (b workerBinding) Tick(ctx context.Context, tick sdk.WorkerTick) (sdk.WorkerTickResult, error) {
	result, err := b.runner.Tick(ctx, lifecycleapplication.WorkerTick{LeaseOwner: tick.LeaseOwner, BatchSize: tick.BatchSize, JobLimit: tick.JobLimit, Now: tick.Now, Scope: tick.Scope})
	return sdk.WorkerTickResult{ProcessedJobs: result.ProcessedJobs, DeletedArtifacts: result.DeletedArtifacts}, adaptError(err)
}

type archiveStoreBinding struct{ writer infra.ArchiveWriter }

func (b archiveStoreBinding) ArchivePayload(ctx context.Context, workspaceID string, job sdkmodel.CleanupJob, policy sdkmodel.PolicyVersion, sourceTable, resourceID string, payload []byte) (bool, error) {
	internalJob, err := convert[internalmodel.CleanupJob](job)
	if err != nil {
		return false, err
	}
	internalPolicy, err := convert[internalmodel.PolicyVersion](policy)
	if err != nil {
		return false, err
	}
	return b.writer.ArchivePayload(ctx, workspaceID, internalJob, internalPolicy, sourceTable, resourceID, payload)
}
func (b archiveStoreBinding) Archived(ctx context.Context, workspaceID, sourceTable, resourceID, policyKey string) (bool, error) {
	return b.writer.Archived(ctx, workspaceID, sourceTable, resourceID, policyKey)
}

func convertResult[T any](value any, sourceErr error) (T, error) {
	if sourceErr != nil {
		var zero T
		return zero, adaptError(sourceErr)
	}
	return convert[T](value)
}

func convert[T any](value any) (T, error) {
	var result T
	payload, err := json.Marshal(value)
	if err != nil {
		return result, &sdk.Error{StatusCode: 500, Code: "lifecycle.contract_encode_failed", Cause: err}
	}
	if err := json.Unmarshal(payload, &result); err != nil {
		return result, &sdk.Error{StatusCode: 500, Code: "lifecycle.contract_decode_failed", Cause: err}
	}
	return result, nil
}

func adaptError(err error) error {
	if err == nil {
		return nil
	}
	var target *sdk.Error
	if errors.As(err, &target) {
		return err
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	status, code := 500, "lifecycle.internal"
	switch {
	case strings.Contains(message, "permission_denied") || strings.Contains(message, "scope mismatch"):
		status, code = 403, "auth.permission_denied"
	case strings.Contains(message, "not found"):
		status, code = 404, "lifecycle.not_found"
	case strings.Contains(message, "unavailable"):
		status, code = 503, "lifecycle.unavailable"
	case strings.Contains(message, "independent approval"):
		status, code = 409, "lifecycle.subject_request.independent_approval_required"
	case strings.Contains(message, "transition") || strings.Contains(message, "lease is still active") || strings.Contains(message, "blocked"):
		status, code = 409, "lifecycle.conflict"
	case strings.Contains(message, "required") || strings.Contains(message, "unsupported") || strings.Contains(message, "invalid") || strings.Contains(message, "cannot") || strings.Contains(message, "must"):
		status, code = 400, "lifecycle.invalid_request"
	}
	return &sdk.Error{StatusCode: status, Code: code, Cause: err}
}

var _ sdk.Binding = (*Binding)(nil)
var _ modulehttp.Provider = (*Binding)(nil)
var _ actioncontract.Provider = (*Binding)(nil)
var _ sdk.Governance = governanceBinding{}
var _ sdk.System = systemBinding{}
var _ sdk.LocalWorkers = workerBinding{}
var _ contract.ArchiveStore = archiveStoreBinding{}
