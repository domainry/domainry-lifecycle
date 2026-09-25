package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	sharedartifact "github.com/domainry/domainry-foundation/artifact"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	"github.com/domainry/domainry-lifecycle-sdk/modulehost"
	filesystem "github.com/domainry/domainry-lifecycle/internal/infrastructure/artifact/filesystem"
)

type SubjectArtifactStore struct {
	*filesystem.SubjectStore
	host      modulehost.Host
	artifacts sharedartifact.ManagedStore
	content   lifecyclecontract.ArtifactContentStore
	writer    lifecyclecontract.ArtifactContentWriter
}

func NewSubjectArtifactStore(host modulehost.Host, root string, content ...lifecyclecontract.ArtifactContentStore) *SubjectArtifactStore {
	var contentStore lifecyclecontract.ArtifactContentStore
	if len(content) > 0 {
		contentStore = content[0]
	}
	result := &SubjectArtifactStore{host: host}
	if artifactHost, ok := host.(artifactPersistenceHost); ok {
		result.artifacts = artifactHost.ArtifactStore()
		result.writer = artifactHost.ArtifactContentWriter()
		if contentStore == nil {
			contentStore = artifactHost.ArtifactContentStore()
		}
	}
	result.content = contentStore
	result.SubjectStore = filesystem.NewSubjectStore(root, contentStore)
	return result
}

type subjectExportArtifactMetadata struct {
	RequestID        string `json:"request_id"`
	ResolvedIdentity string `json:"resolved_identity"`
}

func subjectExportArtifactID(workspaceID, requestID string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(workspaceID) + "\x00" + strings.TrimSpace(requestID)))
	return "lifecycle_subject_export_" + hex.EncodeToString(digest[:])
}

func (s *SubjectArtifactStore) PutSubjectExport(ctx context.Context, input lifecyclecontract.SubjectExportWrite) (result lifecyclecontract.SubjectExportReference, err error) {
	input.WorkspaceID, input.RequestID = strings.TrimSpace(input.WorkspaceID), strings.TrimSpace(input.RequestID)
	input.ResolvedIdentity, input.CreatedBy, input.OwnerOrgID = strings.TrimSpace(input.ResolvedIdentity), strings.TrimSpace(input.CreatedBy), strings.TrimSpace(input.OwnerOrgID)
	input.ExpiresAt = input.ExpiresAt.UTC().Truncate(time.Millisecond)
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if input.WorkspaceID == "" || input.RequestID == "" || input.ResolvedIdentity == "" || input.CreatedBy == "" || !json.Valid(input.Payload) || !input.ExpiresAt.After(time.Now().UTC()) {
		return result, fmt.Errorf("subject export identity, payload and future expiry are required")
	}
	if s.host == nil || s.host.Transactions() == nil || s.artifacts == nil || s.content == nil || s.writer == nil {
		return result, fmt.Errorf("lifecycle shared subject export Artifact persistence is unavailable")
	}
	artifactID := subjectExportArtifactID(input.WorkspaceID, input.RequestID)
	info, err := s.writer.PutImmutable(ctx, input.WorkspaceID, artifactID, input.Payload)
	if err != nil {
		return result, err
	}
	defer func() {
		if err == nil {
			return
		}
		current, found, readErr := s.artifacts.ByID(context.WithoutCancel(ctx), input.WorkspaceID, artifactID)
		if readErr == nil && (!found || current.StorageReference != info.Reference) {
			_ = s.content.Delete(context.WithoutCancel(ctx), input.WorkspaceID, info.Reference)
		}
	}()
	digest := sha256.Sum256(input.Payload)
	if info.Reference == "" || info.Size != int64(len(input.Payload)) || !strings.EqualFold(info.SHA256, hex.EncodeToString(digest[:])) {
		return result, fmt.Errorf("lifecycle subject export content evidence mismatch")
	}
	metadata, err := json.Marshal(subjectExportArtifactMetadata{RequestID: input.RequestID, ResolvedIdentity: input.ResolvedIdentity})
	if err != nil {
		return result, err
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	value := sharedartifact.Artifact{
		ID: artifactID, WorkspaceID: input.WorkspaceID, Owner: sharedartifact.OwnerLifecycle, Kind: "subject_export", IdempotencyKey: artifactID,
		CreatedBy: input.CreatedBy, OwnerOrgID: input.OwnerOrgID, Filename: artifactID + ".json", MediaType: "application/json",
		ContentSHA256: info.SHA256, SizeBytes: info.Size, StorageReference: info.Reference,
		Status: sharedartifact.StatusAvailable, ExpiresAt: input.ExpiresAt, ScanStatus: sharedartifact.ScanNotRequired,
		Metadata: metadata, CreatedAt: now, UpdatedAt: now,
	}
	var stored sharedartifact.Artifact
	err = s.host.Transactions().WithinTransaction(ctx, func(txctx context.Context, _ modulehost.DBTX) error {
		artifactCtx := lifecycleArtifactContext(txctx)
		var registerErr error
		stored, _, registerErr = s.artifacts.Register(artifactCtx, value)
		if registerErr != nil {
			return fmt.Errorf("register Lifecycle subject export Artifact: %w", registerErr)
		}
		if stored.Owner != sharedartifact.OwnerLifecycle || stored.Kind != "subject_export" || stored.Status != sharedartifact.StatusAvailable || stored.StorageReference != info.Reference || stored.ContentSHA256 != info.SHA256 || stored.SizeBytes != info.Size || stored.ExpiresAt.IsZero() {
			return fmt.Errorf("Lifecycle subject export Artifact identity conflict")
		}
		bindings := []sharedartifact.Binding{
			{ID: artifactID + ":request", WorkspaceID: input.WorkspaceID, ArtifactID: artifactID, Owner: sharedartifact.OwnerLifecycle, Kind: sharedartifact.BindingJob, ResourceType: "lifecycle.subject_request", ResourceID: input.RequestID, Metadata: json.RawMessage(`{}`), CreatedAt: now},
			{ID: artifactID + ":subject", WorkspaceID: input.WorkspaceID, ArtifactID: artifactID, Owner: sharedartifact.OwnerLifecycle, Kind: sharedartifact.BindingSubject, ResourceType: "data_subject", ResourceID: input.ResolvedIdentity, Metadata: json.RawMessage(`{}`), CreatedAt: now},
		}
		for _, binding := range bindings {
			if _, _, bindErr := s.artifacts.Bind(artifactCtx, binding); bindErr != nil {
				return fmt.Errorf("bind Lifecycle subject export Artifact: %w", bindErr)
			}
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	return lifecyclecontract.SubjectExportReference{ArtifactID: stored.ID, ExpiresAt: stored.ExpiresAt}, nil
}

func (s *SubjectArtifactStore) ReadSubjectExport(ctx context.Context, workspaceID, artifactID string, now time.Time) (json.RawMessage, error) {
	workspaceID, artifactID, now = strings.TrimSpace(workspaceID), strings.TrimSpace(artifactID), now.UTC()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if workspaceID == "" || artifactID == "" || now.IsZero() || s.artifacts == nil || s.content == nil {
		return nil, fmt.Errorf("lifecycle shared subject export Artifact persistence is unavailable")
	}
	value, found, err := s.artifacts.ByID(lifecycleArtifactContext(ctx), workspaceID, artifactID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("subject export unavailable or expired")
	}
	if value.Owner != sharedartifact.OwnerLifecycle || value.Kind != "subject_export" || value.Status != sharedartifact.StatusAvailable || value.ExpiresAt.IsZero() || !now.Before(value.ExpiresAt) || value.SizeBytes < 0 || value.StorageReference == "" {
		return nil, fmt.Errorf("subject export unavailable or expired")
	}
	reader, err := s.content.Open(ctx, workspaceID, value.StorageReference)
	if err != nil {
		return nil, err
	}
	raw, readErr := io.ReadAll(io.LimitReader(reader, value.SizeBytes+1))
	closeErr := reader.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	digest := sha256.Sum256(raw)
	if int64(len(raw)) != value.SizeBytes || !strings.EqualFold(hex.EncodeToString(digest[:]), value.ContentSHA256) || !json.Valid(raw) {
		return nil, fmt.Errorf("subject export content evidence mismatch")
	}
	current, found, err := s.artifacts.ByID(lifecycleArtifactContext(ctx), workspaceID, artifactID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("subject export changed during read")
	}
	if current.Status != sharedartifact.StatusAvailable || current.StorageReference != value.StorageReference || current.ContentSHA256 != value.ContentSHA256 || current.SizeBytes != value.SizeBytes || current.ExpiresAt != value.ExpiresAt || !now.Before(current.ExpiresAt) {
		return nil, fmt.Errorf("subject export changed during read")
	}
	return append(json.RawMessage(nil), raw...), nil
}

func (s *SubjectArtifactStore) DeleteExpiredSubjectExports(ctx context.Context, now time.Time) (int, error) {
	service, err := sharedartifact.NewCleanupService(s.artifacts, s.content)
	if err != nil {
		return 0, err
	}
	result, err := service.Reconcile(lifecycleArtifactContext(ctx), sharedartifact.OwnerLifecycle, "subject_export", now, 500)
	return result.Deleted, err
}

func (s *SubjectArtifactStore) DeleteSubjectExport(ctx context.Context, workspaceID, artifactID string) error {
	workspaceID, artifactID = strings.TrimSpace(workspaceID), strings.TrimSpace(artifactID)
	if workspaceID == "" || artifactID == "" || s.artifacts == nil || s.content == nil {
		return fmt.Errorf("lifecycle shared subject export Artifact persistence is unavailable")
	}
	artifactCtx := lifecycleArtifactContext(ctx)
	value, found, err := s.artifacts.ByID(artifactCtx, workspaceID, artifactID)
	if err != nil {
		return err
	}
	if !found || value.Owner != sharedartifact.OwnerLifecycle || value.Kind != "subject_export" {
		return fmt.Errorf("subject export unavailable")
	}
	now := time.Now().UTC()
	if value.Status == sharedartifact.StatusAvailable {
		changed, transitionErr := s.artifacts.Transition(artifactCtx, workspaceID, artifactID, sharedartifact.StatusAvailable, sharedartifact.StatusExpired, value.ScanStatus, now)
		if transitionErr != nil {
			return transitionErr
		}
		if changed {
			value.Status = sharedartifact.StatusExpired
		} else {
			value, found, err = s.artifacts.ByID(artifactCtx, workspaceID, artifactID)
			if err != nil {
				return err
			}
			if !found {
				return fmt.Errorf("subject export unavailable")
			}
		}
	}
	if value.Status == sharedartifact.StatusDeleted {
		return nil
	}
	if value.Status != sharedartifact.StatusExpired && value.Status != sharedartifact.StatusRejected {
		return fmt.Errorf("subject export is not in a deletable terminal state")
	}
	if err := s.content.Delete(ctx, workspaceID, value.StorageReference); err != nil && !errors.Is(err, sharedartifact.ErrContentNotFound) {
		return err
	}
	changed, err := s.artifacts.Transition(artifactCtx, workspaceID, artifactID, value.Status, sharedartifact.StatusDeleted, value.ScanStatus, now)
	if err != nil || changed {
		return err
	}
	current, found, err := s.artifacts.ByID(artifactCtx, workspaceID, artifactID)
	if err != nil {
		return err
	}
	if !found || current.Status != sharedartifact.StatusDeleted {
		return fmt.Errorf("subject export changed while deleting")
	}
	return nil
}

func (s *SubjectArtifactStore) DeleteSubjectFileVersion(ctx context.Context, ref lifecyclecontract.SubjectFileReference, expected lifecyclecontract.SubjectFileEvidence) (lifecyclecontract.SubjectFileEvidence, error) {
	if s.host == nil || s.host.Transactions() == nil || s.artifacts == nil {
		return lifecyclecontract.SubjectFileEvidence{}, fmt.Errorf("subject file registry transaction unavailable")
	}
	var terminal sharedartifact.Artifact
	registered := false
	err := s.host.Transactions().WithinTransaction(ctx, func(txctx context.Context, _ modulehost.DBTX) error {
		values, err := s.artifacts.List(lifecycleArtifactContext(txctx), ref.WorkspaceID, sharedartifact.Query{
			Owner: sharedartifact.OwnerUploads, Kind: "file", StorageReference: expected.Filename, Limit: 2,
		})
		if err != nil {
			return err
		}
		if len(values) > 1 {
			return fmt.Errorf("subject file registry identity is not unique")
		}
		if len(values) == 0 || values[0].Status == sharedartifact.StatusDeleted {
			return nil
		}
		terminal, registered = values[0], true
		if terminal.ContentSHA256 != expected.SHA256 || terminal.SizeBytes != expected.Size {
			return fmt.Errorf("subject file registry version changed")
		}
		if terminal.Status == sharedartifact.StatusExpired || terminal.Status == sharedartifact.StatusRejected {
			return nil
		}
		metadata, err := decodeUploadArtifactMetadata(terminal.Metadata)
		if err != nil {
			return err
		}
		metadata.ReferenceState = "expired"
		raw, err := encodeUploadArtifactMetadata(metadata)
		if err != nil {
			return err
		}
		changed, err := s.artifacts.Update(lifecycleArtifactContext(txctx), sharedartifact.Mutation{
			WorkspaceID: terminal.WorkspaceID, ID: terminal.ID, Owner: terminal.Owner, Kind: terminal.Kind,
			ExpectedStatus: terminal.Status, ExpectedScanStatus: terminal.ScanStatus,
			Status: sharedartifact.StatusExpired, ScanStatus: terminal.ScanStatus, ExpiresAt: terminal.ExpiresAt,
			Metadata: raw, UpdatedAt: time.Now().UTC(),
		})
		if err != nil {
			return err
		}
		if !changed {
			return fmt.Errorf("subject file registry version changed")
		}
		terminal.Status = sharedartifact.StatusExpired
		terminal.Metadata = raw
		return nil
	})
	if err != nil {
		return lifecyclecontract.SubjectFileEvidence{}, err
	}
	result, err := s.SubjectStore.DeleteSubjectFileVersion(ctx, ref, expected)
	if err != nil || !registered {
		return result, err
	}
	err = s.host.Transactions().WithinTransaction(ctx, func(txctx context.Context, _ modulehost.DBTX) error {
		current, found, err := s.artifacts.ByID(lifecycleArtifactContext(txctx), terminal.WorkspaceID, terminal.ID)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("subject file registry disappeared during deletion")
		}
		if current.Status == sharedartifact.StatusDeleted {
			return nil
		}
		if current.Status != sharedartifact.StatusExpired && current.Status != sharedartifact.StatusRejected {
			return fmt.Errorf("subject file registry left terminal state during deletion")
		}
		if current.ContentSHA256 != expected.SHA256 || current.SizeBytes != expected.Size || current.StorageReference != expected.Filename {
			return fmt.Errorf("subject file registry version changed")
		}
		metadata, err := decodeUploadArtifactMetadata(current.Metadata)
		if err != nil {
			return err
		}
		metadata.ReferenceState = "deleted"
		raw, err := encodeUploadArtifactMetadata(metadata)
		if err != nil {
			return err
		}
		changed, err := s.artifacts.Update(lifecycleArtifactContext(txctx), sharedartifact.Mutation{
			WorkspaceID: current.WorkspaceID, ID: current.ID, Owner: current.Owner, Kind: current.Kind,
			ExpectedStatus: current.Status, ExpectedScanStatus: current.ScanStatus,
			Status: sharedartifact.StatusDeleted, ScanStatus: current.ScanStatus, Metadata: raw, UpdatedAt: time.Now().UTC(),
		})
		if err != nil {
			return err
		}
		if !changed {
			latest, latestFound, readErr := s.artifacts.ByID(lifecycleArtifactContext(txctx), current.WorkspaceID, current.ID)
			if readErr != nil {
				return readErr
			}
			if !latestFound || latest.Status != sharedartifact.StatusDeleted {
				return fmt.Errorf("subject file registry version changed")
			}
		}
		return nil
	})
	return result, err
}

var _ lifecyclecontract.SubjectFileVersionDeleter = (*SubjectArtifactStore)(nil)
