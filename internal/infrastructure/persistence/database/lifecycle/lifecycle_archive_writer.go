package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	sharedartifact "github.com/domainry/domainry-foundation/artifact"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	"github.com/domainry/domainry-lifecycle-sdk/modulehost"
	lifecyclemodel "github.com/domainry/domainry-lifecycle/internal/domain/lifecycle/model"
)

type archiveArtifactMetadata struct {
	Owner         string `json:"owner"`
	SourceTable   string `json:"source_table"`
	ResourceID    string `json:"resource_id"`
	PolicyKey     string `json:"policy_key"`
	PolicyVersion string `json:"policy_version"`
	JobID         string `json:"job_id"`
}

type ArchiveWriter struct {
	artifacts sharedartifact.ManagedStore
	content   lifecyclecontract.ArtifactContentWriter
	cleanup   lifecyclecontract.ArtifactContentStore
}

func NewArchiveWriter(host modulehost.Host) ArchiveWriter {
	artifactHost, _ := host.(modulehost.ArtifactStoreHost)
	if artifactHost == nil {
		return ArchiveWriter{}
	}
	return ArchiveWriter{artifacts: artifactHost.ArtifactStore(), content: artifactHost.ArtifactContentWriter(), cleanup: artifactHost.ArtifactContentStore()}
}

func archiveArtifactID(workspaceID, source, resourceID, policyKey string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(workspaceID) + "\x00" + strings.TrimSpace(policyKey) + "\x00" + strings.TrimSpace(source) + "\x00" + strings.TrimSpace(resourceID)))
	return "lifecycle_archive_" + hex.EncodeToString(digest[:])
}

func (w ArchiveWriter) Archived(ctx context.Context, workspaceID, source, resourceID, policyKey string) (bool, error) {
	if w.artifacts == nil {
		return false, fmt.Errorf("lifecycle shared Artifact store unavailable")
	}
	value, found, err := w.artifacts.ByID(lifecycleArtifactContext(ctx), workspaceID, archiveArtifactID(workspaceID, source, resourceID, policyKey))
	if err != nil || !found {
		return false, err
	}
	if value.Owner != sharedartifact.OwnerLifecycle || value.Kind != "archive" || value.Status != sharedartifact.StatusAvailable {
		return false, nil
	}
	bindings, err := w.artifacts.Bindings(lifecycleArtifactContext(ctx), workspaceID, value.ID)
	if err != nil {
		return false, err
	}
	for _, binding := range bindings {
		if binding.Owner == sharedartifact.OwnerLifecycle && binding.Kind == sharedartifact.BindingObjectField && binding.ResourceType == strings.TrimSpace(source) && binding.ResourceID == strings.TrimSpace(resourceID) && binding.FieldKey == strings.TrimSpace(policyKey) {
			return true, nil
		}
	}
	return false, nil
}

func (w ArchiveWriter) ArchivePayload(ctx context.Context, owner string, job lifecyclemodel.CleanupJob, policy lifecyclemodel.PolicyVersion, source, resourceID string, payload []byte) (createdResult bool, err error) {
	if w.artifacts == nil || w.content == nil || w.cleanup == nil {
		return false, fmt.Errorf("lifecycle shared Artifact store or content writer unavailable")
	}
	artifactID := archiveArtifactID(job.WorkspaceID, source, resourceID, policy.Policy.Key)
	if existing, err := w.Archived(ctx, job.WorkspaceID, source, resourceID, policy.Policy.Key); err != nil || existing {
		return false, err
	}
	archivedAt := job.UpdatedAt.UTC()
	if archivedAt.IsZero() {
		archivedAt = time.Now().UTC()
	}
	content, err := w.content.PutImmutable(ctx, job.WorkspaceID, artifactID, payload)
	if err != nil {
		return false, err
	}
	defer func() {
		if err == nil {
			return
		}
		current, found, readErr := w.artifacts.ByID(context.WithoutCancel(ctx), job.WorkspaceID, artifactID)
		if readErr == nil && (!found || current.StorageReference != content.Reference) {
			_ = w.cleanup.Delete(context.WithoutCancel(ctx), job.WorkspaceID, content.Reference)
		}
	}()
	digest := sha256.Sum256(payload)
	if content.Reference == "" || !strings.EqualFold(content.SHA256, hex.EncodeToString(digest[:])) || content.Size != int64(len(payload)) {
		return false, fmt.Errorf("lifecycle archive content evidence mismatch")
	}
	metadata, err := json.Marshal(archiveArtifactMetadata{
		Owner: strings.TrimSpace(owner), SourceTable: strings.TrimSpace(source), ResourceID: strings.TrimSpace(resourceID),
		PolicyKey: policy.Policy.Key, PolicyVersion: policy.Policy.Version, JobID: job.ID,
	})
	if err != nil {
		return false, err
	}
	createdBy := strings.TrimSpace(job.RequestedBy)
	if createdBy == "" {
		createdBy = "lifecycle_cleanup"
	}
	value := sharedartifact.Artifact{
		ID: artifactID, WorkspaceID: job.WorkspaceID, Owner: sharedartifact.OwnerLifecycle, Kind: "archive", IdempotencyKey: artifactID,
		CreatedBy: createdBy, OwnerOrgID: strings.TrimSpace(job.OwnerOrgID), Filename: artifactID + ".json", MediaType: "application/json",
		ContentSHA256: content.SHA256, SizeBytes: content.Size, StorageReference: content.Reference,
		Status: sharedartifact.StatusAvailable, ScanStatus: sharedartifact.ScanNotRequired, Metadata: metadata, CreatedAt: archivedAt, UpdatedAt: archivedAt,
	}
	artifactCtx := lifecycleArtifactContext(ctx)
	_, created, err := w.artifacts.Register(artifactCtx, value)
	if err != nil {
		return false, fmt.Errorf("register Lifecycle archive Artifact: %w", err)
	}
	binding := sharedartifact.Binding{
		ID: artifactID + ":job", WorkspaceID: job.WorkspaceID, ArtifactID: artifactID, Owner: sharedartifact.OwnerLifecycle,
		Kind: sharedartifact.BindingJob, ResourceType: "lifecycle.cleanup_job", ResourceID: job.ID, Metadata: json.RawMessage(`{}`), CreatedAt: archivedAt,
	}
	_, jobBindingCreated, err := w.artifacts.Bind(artifactCtx, binding)
	if err != nil {
		return false, fmt.Errorf("bind Lifecycle archive Artifact: %w", err)
	}
	sourceBinding := sharedartifact.Binding{
		ID: artifactID + ":source", WorkspaceID: job.WorkspaceID, ArtifactID: artifactID, Owner: sharedartifact.OwnerLifecycle,
		Kind: sharedartifact.BindingObjectField, ResourceType: strings.TrimSpace(source), ResourceID: strings.TrimSpace(resourceID),
		FieldKey: policy.Policy.Key, Metadata: json.RawMessage(`{}`), CreatedAt: archivedAt,
	}
	_, sourceBindingCreated, err := w.artifacts.Bind(artifactCtx, sourceBinding)
	if err != nil {
		return false, fmt.Errorf("bind Lifecycle archive source Artifact: %w", err)
	}
	return created || jobBindingCreated || sourceBindingCreated, nil
}

func lifecycleArtifactContext(ctx context.Context) context.Context {
	if executor := modulehost.ExecutorFromContext(ctx, nil); executor != nil {
		return sharedartifact.WithExecutor(ctx, executor)
	}
	return ctx
}
