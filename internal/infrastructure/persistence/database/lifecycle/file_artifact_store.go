package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	sharedartifact "github.com/domainry/domainry-foundation/artifact"
	requestcontext "github.com/domainry/domainry-foundation/requestcontext"
	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	"github.com/domainry/domainry-lifecycle-sdk/modulehost"
)

const uploadArtifactGracePeriod = 24 * time.Hour

var errUploadArtifactIdentityConflict = errors.New("lifecycle upload artifact identity conflict")

type uploadArtifactMetadata struct {
	ObjectKey        string `json:"object_key"`
	FieldKey         string `json:"field_key"`
	ReferenceState   string `json:"reference_state"`
	ScanProvider     string `json:"scan_provider,omitempty"`
	ScanEvidenceRef  string `json:"scan_evidence_ref,omitempty"`
	ScannedAt        int64  `json:"scanned_at,omitempty"`
	LastReferencedAt int64  `json:"last_referenced_at,omitempty"`
}

type FileArtifactStore struct {
	artifacts  sharedartifact.ManagedStore
	fields     lifecyclecontract.UploadFieldCatalog
	references lifecyclecontract.UploadArtifactReferenceResolver
	cleaner    lifecyclecontract.ExpiredUploadReferenceCleaner
	content    lifecyclecontract.ArtifactContentStore
	contextErr func(context.Context) error
}

type FileArtifactStoreOption func(*FileArtifactStore)

func WithUploadArtifactReferences(references lifecyclecontract.UploadArtifactReferenceResolver) FileArtifactStoreOption {
	return func(store *FileArtifactStore) { store.references = references }
}

func WithExpiredUploadReferenceCleaner(cleaner lifecyclecontract.ExpiredUploadReferenceCleaner) FileArtifactStoreOption {
	return func(store *FileArtifactStore) { store.cleaner = cleaner }
}

func WithArtifactContentStore(content lifecyclecontract.ArtifactContentStore) FileArtifactStoreOption {
	return func(store *FileArtifactStore) { store.content = content }
}

func NewFileArtifactStore(host modulehost.Host, fields lifecyclecontract.UploadFieldCatalog, _ string, options ...FileArtifactStoreOption) *FileArtifactStore {
	result := &FileArtifactStore{fields: fields, contextErr: func(ctx context.Context) error { return ctx.Err() }}
	if artifactHost, ok := host.(artifactPersistenceHost); ok {
		result.artifacts = artifactHost.ArtifactStore()
		result.content = artifactHost.ArtifactContentStore()
	}
	for _, option := range options {
		if option != nil {
			option(result)
		}
	}
	return result
}

func (s *FileArtifactStore) RegisterUpload(ctx context.Context, value lifecyclecontract.UploadArtifact) error {
	if s == nil || s.artifacts == nil {
		return fmt.Errorf("upload Artifact store unavailable")
	}
	workspace, err := lifecycleaccess.NewWorkspaceID(value.WorkspaceID)
	if err != nil {
		return err
	}
	value.WorkspaceID = workspace.String()
	value.ID, value.ObjectKey, value.FieldKey, value.Filename = strings.TrimSpace(value.ID), strings.TrimSpace(value.ObjectKey), strings.TrimSpace(value.FieldKey), strings.TrimSpace(value.Filename)
	value.ContentType, value.SHA256 = strings.TrimSpace(value.ContentType), strings.ToLower(strings.TrimSpace(value.SHA256))
	if value.ID == "" {
		value.ID = requestcontext.NewRequestID()
	}
	if s.fields == nil || !s.fields.HasUploadField(value.ObjectKey, value.FieldKey) {
		return fmt.Errorf("upload artifact owner field is not declared")
	}
	if value.Filename == "" || filepath.Base(value.Filename) != value.Filename || value.SHA256 == "" || value.Size < 0 || value.CreatedAt.IsZero() {
		return fmt.Errorf("upload artifact evidence is incomplete")
	}
	if existing, found, lookupErr := s.artifacts.ByID(lifecycleArtifactContext(ctx), value.WorkspaceID, value.ID); lookupErr != nil {
		return lookupErr
	} else if found {
		registered, decodeErr := uploadArtifactFromShared(existing)
		if decodeErr == nil && sameUploadArtifactIdentity(registered, value) {
			return nil
		}
		return errUploadArtifactIdentityConflict
	}
	byFilename, err := s.artifacts.List(lifecycleArtifactContext(ctx), value.WorkspaceID, sharedartifact.Query{Owner: sharedartifact.OwnerUploads, Kind: "file", Filename: value.Filename, Limit: 1})
	if err != nil {
		return err
	}
	if len(byFilename) != 0 {
		return errUploadArtifactIdentityConflict
	}
	metadata, err := encodeUploadArtifactMetadata(uploadArtifactMetadata{ObjectKey: value.ObjectKey, FieldKey: value.FieldKey, ReferenceState: "staged"})
	if err != nil {
		return err
	}
	mediaType := value.ContentType
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	createdAt := value.CreatedAt.UTC()
	artifact := sharedartifact.Artifact{
		ID: value.ID, WorkspaceID: value.WorkspaceID, Owner: sharedartifact.OwnerUploads, Kind: "file", IdempotencyKey: value.ID,
		CreatedBy: "runtime_upload", Filename: value.Filename, MediaType: mediaType, ContentSHA256: value.SHA256, SizeBytes: value.Size,
		StorageReference: value.Filename, Status: sharedartifact.StatusPending, ExpiresAt: createdAt.Add(uploadArtifactGracePeriod),
		ScanStatus: sharedartifact.ScanPending, Metadata: metadata, CreatedAt: createdAt, UpdatedAt: createdAt,
	}
	_, _, err = s.artifacts.Register(lifecycleArtifactContext(ctx), artifact)
	if errors.Is(err, sharedartifact.ErrIdentityConflict) {
		return errUploadArtifactIdentityConflict
	}
	return err
}

func sameUploadArtifactIdentity(left, right lifecyclecontract.UploadArtifact) bool {
	leftType, rightType := strings.TrimSpace(left.ContentType), strings.TrimSpace(right.ContentType)
	if leftType == "application/octet-stream" && rightType == "" {
		rightType = leftType
	}
	return left.ID == right.ID && left.WorkspaceID == right.WorkspaceID && left.ObjectKey == right.ObjectKey && left.FieldKey == right.FieldKey && left.Filename == right.Filename &&
		strings.EqualFold(leftType, rightType) && strings.EqualFold(strings.TrimSpace(left.SHA256), strings.TrimSpace(right.SHA256)) && left.Size == right.Size
}

func (s *FileArtifactStore) FindFileScan(ctx context.Context, workspaceID, fileID string) (lifecyclecontract.FileScanEvidence, error) {
	if s == nil || s.artifacts == nil {
		return lifecyclecontract.FileScanEvidence{}, fmt.Errorf("upload Artifact store unavailable")
	}
	workspace, err := lifecycleaccess.NewWorkspaceID(workspaceID)
	if err != nil {
		return lifecyclecontract.FileScanEvidence{}, err
	}
	artifact, found, err := s.artifacts.ByID(lifecycleArtifactContext(ctx), workspace.String(), strings.TrimSpace(fileID))
	if err != nil {
		return lifecyclecontract.FileScanEvidence{}, err
	}
	if !found || artifact.Owner != sharedartifact.OwnerUploads || artifact.Kind != "file" || artifact.Status == sharedartifact.StatusDeleted {
		return lifecyclecontract.FileScanEvidence{}, sql.ErrNoRows
	}
	return fileScanEvidenceFromShared(artifact)
}

func (s *FileArtifactStore) PendingFileScans(ctx context.Context, scope lifecycleaccess.SystemScope, limit int) ([]lifecyclecontract.FileScanEvidence, error) {
	if s == nil || s.artifacts == nil {
		return nil, fmt.Errorf("upload Artifact store unavailable")
	}
	if _, err := lifecycleaccess.NewSystemQueryScope(scope); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	artifacts, err := s.artifacts.List(lifecycleArtifactContext(ctx), "", sharedartifact.Query{
		Owner: sharedartifact.OwnerUploads, Kind: "file", Statuses: []sharedartifact.Status{sharedartifact.StatusPending, sharedartifact.StatusAvailable},
		ScanStatuses: []sharedartifact.ScanStatus{sharedartifact.ScanPending}, Limit: limit,
	})
	if err != nil {
		return nil, err
	}
	result := make([]lifecyclecontract.FileScanEvidence, 0, len(artifacts))
	for _, artifact := range artifacts {
		evidence, err := fileScanEvidenceFromShared(artifact)
		if err != nil {
			return nil, err
		}
		result = append(result, evidence)
	}
	return result, nil
}

func (s *FileArtifactStore) RecordFileScan(ctx context.Context, evidence lifecyclecontract.FileScanEvidence) error {
	nextScan, err := sharedScanStatus(evidence.Status)
	if err != nil || nextScan == sharedartifact.ScanPending {
		return fmt.Errorf("terminal file scan status required")
	}
	if strings.TrimSpace(evidence.Provider) == "" || strings.TrimSpace(evidence.EvidenceRef) == "" || evidence.ScannedAt.IsZero() {
		return fmt.Errorf("file scan evidence is incomplete")
	}
	currentEvidence, err := s.FindFileScan(ctx, evidence.WorkspaceID, evidence.FileID)
	if err != nil {
		return err
	}
	if currentEvidence.SHA256 != strings.TrimSpace(evidence.SHA256) || currentEvidence.Size != evidence.Size {
		return fmt.Errorf("file scan content identity mismatch")
	}
	if currentEvidence.Status != lifecyclecontract.FileScanPending {
		if currentEvidence.Status == evidence.Status && currentEvidence.Provider == strings.TrimSpace(evidence.Provider) && currentEvidence.EvidenceRef == strings.TrimSpace(evidence.EvidenceRef) {
			return nil
		}
		return fmt.Errorf("file scan already has terminal evidence")
	}
	artifact, found, err := s.artifacts.ByID(lifecycleArtifactContext(ctx), currentEvidence.WorkspaceID, currentEvidence.FileID)
	if err != nil {
		return err
	}
	if !found {
		return sql.ErrNoRows
	}
	metadata, err := decodeUploadArtifactMetadata(artifact.Metadata)
	if err != nil {
		return err
	}
	metadata.ScanProvider, metadata.ScanEvidenceRef, metadata.ScannedAt = strings.TrimSpace(evidence.Provider), strings.TrimSpace(evidence.EvidenceRef), evidence.ScannedAt.UTC().UnixMilli()
	raw, err := encodeUploadArtifactMetadata(metadata)
	if err != nil {
		return err
	}
	nextStatus := artifact.Status
	if nextScan == sharedartifact.ScanRejected {
		nextStatus = sharedartifact.StatusRejected
	}
	changed, err := s.artifacts.Update(lifecycleArtifactContext(ctx), sharedartifact.Mutation{
		WorkspaceID: artifact.WorkspaceID, ID: artifact.ID, Owner: artifact.Owner, Kind: artifact.Kind,
		ExpectedStatus: artifact.Status, ExpectedScanStatus: sharedartifact.ScanPending, Status: nextStatus, ScanStatus: nextScan,
		ExpiresAt: artifact.ExpiresAt, Metadata: raw, UpdatedAt: evidence.ScannedAt.UTC(),
	})
	if err != nil {
		return err
	}
	if !changed {
		latest, findErr := s.FindFileScan(ctx, evidence.WorkspaceID, evidence.FileID)
		if findErr == nil && latest.Status == evidence.Status && latest.Provider == strings.TrimSpace(evidence.Provider) && latest.EvidenceRef == strings.TrimSpace(evidence.EvidenceRef) {
			return nil
		}
		return fmt.Errorf("file scan already has terminal evidence")
	}
	return nil
}

func (s *FileArtifactStore) ReconcileUploadArtifacts(ctx context.Context, scope lifecycleaccess.SystemScope, now time.Time, limit int) (lifecyclecontract.UploadCleanupResult, error) {
	result := lifecyclecontract.UploadCleanupResult{}
	if s == nil || s.artifacts == nil || s.content == nil {
		return result, fmt.Errorf("upload Artifact store or content store unavailable")
	}
	if _, err := lifecycleaccess.NewSystemCommandScope(scope); err != nil {
		return result, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	if s.cleaner != nil {
		expired, err := s.cleaner.ExpireUploadReferences(ctx, now, limit)
		if err != nil {
			return result, err
		}
		result.ExpiredDownloads = expired
	}
	artifacts, err := s.artifacts.List(lifecycleArtifactContext(ctx), "", sharedartifact.Query{
		Owner: sharedartifact.OwnerUploads, Kind: "file", Statuses: []sharedartifact.Status{sharedartifact.StatusPending, sharedartifact.StatusAvailable, sharedartifact.StatusRejected, sharedartifact.StatusExpired}, Limit: limit,
	})
	if err != nil {
		return result, err
	}
	for _, artifact := range artifacts {
		if err := s.contextErr(ctx); err != nil {
			return result, err
		}
		result.Scanned++
		metadata, err := decodeUploadArtifactMetadata(artifact.Metadata)
		if err != nil {
			return result, err
		}
		if artifact.Status == sharedartifact.StatusExpired || artifact.Status == sharedartifact.StatusRejected {
			deleted, deleteErr := s.deleteTerminalUploadArtifact(ctx, artifact, metadata, now)
			if deleteErr != nil {
				return result, deleteErr
			}
			if deleted {
				result.Deleted++
			}
			continue
		}
		referenced, err := s.artifactReferenced(ctx, artifact.WorkspaceID, metadata.ObjectKey, metadata.FieldKey, artifact.Filename)
		if err != nil {
			return result, err
		}
		if referenced {
			metadata.ReferenceState, metadata.LastReferencedAt = "referenced", now.UTC().UnixMilli()
			if err := s.updateArtifact(ctx, artifact, sharedartifact.StatusAvailable, time.Time{}, metadata, now); err != nil {
				return result, err
			}
			result.Referenced++
			continue
		}
		if metadata.ReferenceState == "referenced" || artifact.ExpiresAt.IsZero() {
			metadata.ReferenceState = "orphaned"
			if err := s.updateArtifact(ctx, artifact, sharedartifact.StatusPending, now.Add(uploadArtifactGracePeriod), metadata, now); err != nil {
				return result, err
			}
			result.Orphaned++
			continue
		}
		if now.Before(artifact.ExpiresAt) {
			continue
		}
		metadata.ReferenceState = "expired"
		if err := s.updateArtifact(ctx, artifact, sharedartifact.StatusExpired, artifact.ExpiresAt, metadata, now); err != nil {
			return result, err
		}
		artifact.Status, artifact.UpdatedAt = sharedartifact.StatusExpired, now.UTC()
		deleted, deleteErr := s.deleteTerminalUploadArtifact(ctx, artifact, metadata, now)
		if deleteErr != nil {
			return result, deleteErr
		}
		if deleted {
			result.Deleted++
		}
	}
	return result, nil
}

func (s *FileArtifactStore) deleteTerminalUploadArtifact(ctx context.Context, artifact sharedartifact.Artifact, metadata uploadArtifactMetadata, now time.Time) (bool, error) {
	if artifact.Status != sharedartifact.StatusExpired && artifact.Status != sharedartifact.StatusRejected {
		return false, fmt.Errorf("upload Artifact must be terminal before content deletion")
	}
	if err := s.content.Delete(ctx, artifact.WorkspaceID, artifact.StorageReference); err != nil && !errors.Is(err, lifecyclecontract.ErrArtifactContentNotFound) {
		return false, err
	}
	metadata.ReferenceState = "deleted"
	raw, err := encodeUploadArtifactMetadata(metadata)
	if err != nil {
		return false, err
	}
	changed, err := s.artifacts.Update(lifecycleArtifactContext(ctx), sharedartifact.Mutation{
		WorkspaceID: artifact.WorkspaceID, ID: artifact.ID, Owner: artifact.Owner, Kind: artifact.Kind,
		ExpectedStatus: artifact.Status, ExpectedScanStatus: artifact.ScanStatus,
		Status: sharedartifact.StatusDeleted, ScanStatus: artifact.ScanStatus, Metadata: raw, UpdatedAt: now.UTC(),
	})
	if err != nil || changed {
		return changed, err
	}
	current, found, err := s.artifacts.ByID(lifecycleArtifactContext(ctx), artifact.WorkspaceID, artifact.ID)
	if err != nil {
		return false, err
	}
	if found && current.Status == sharedartifact.StatusDeleted {
		return false, nil
	}
	return false, fmt.Errorf("upload Artifact state changed while deleting terminal content")
}

func (s *FileArtifactStore) artifactReferenced(ctx context.Context, workspaceID, objectKey, fieldKey, filename string) (bool, error) {
	if s.references == nil {
		return false, nil
	}
	return s.references.UploadArtifactReferenced(ctx, workspaceID, objectKey, fieldKey, filename)
}

func (s *FileArtifactStore) updateArtifact(ctx context.Context, artifact sharedartifact.Artifact, status sharedartifact.Status, expiresAt time.Time, metadata uploadArtifactMetadata, now time.Time) error {
	raw, err := encodeUploadArtifactMetadata(metadata)
	if err != nil {
		return err
	}
	changed, err := s.artifacts.Update(lifecycleArtifactContext(ctx), sharedartifact.Mutation{
		WorkspaceID: artifact.WorkspaceID, ID: artifact.ID, Owner: artifact.Owner, Kind: artifact.Kind,
		ExpectedStatus: artifact.Status, ExpectedScanStatus: artifact.ScanStatus, Status: status, ScanStatus: artifact.ScanStatus,
		ExpiresAt: expiresAt, Metadata: raw, UpdatedAt: now.UTC(),
	})
	if err != nil {
		return err
	}
	if !changed {
		return fmt.Errorf("upload Artifact state changed concurrently")
	}
	return nil
}

func uploadArtifactFromShared(value sharedartifact.Artifact) (lifecyclecontract.UploadArtifact, error) {
	if value.Owner != sharedartifact.OwnerUploads || value.Kind != "file" {
		return lifecyclecontract.UploadArtifact{}, fmt.Errorf("shared Artifact is not an upload file")
	}
	metadata, err := decodeUploadArtifactMetadata(value.Metadata)
	if err != nil {
		return lifecyclecontract.UploadArtifact{}, err
	}
	return lifecyclecontract.UploadArtifact{
		ID: value.ID, WorkspaceID: value.WorkspaceID, ObjectKey: metadata.ObjectKey, FieldKey: metadata.FieldKey,
		Filename: value.Filename, ContentType: value.MediaType, SHA256: value.ContentSHA256, Size: value.SizeBytes, CreatedAt: value.CreatedAt,
	}, nil
}

func fileScanEvidenceFromShared(value sharedartifact.Artifact) (lifecyclecontract.FileScanEvidence, error) {
	upload, err := uploadArtifactFromShared(value)
	if err != nil {
		return lifecyclecontract.FileScanEvidence{}, err
	}
	metadata, err := decodeUploadArtifactMetadata(value.Metadata)
	if err != nil {
		return lifecyclecontract.FileScanEvidence{}, err
	}
	scannedAt := time.Time{}
	if metadata.ScannedAt != 0 {
		scannedAt = time.UnixMilli(metadata.ScannedAt).UTC()
	}
	return lifecyclecontract.FileScanEvidence{
		FileID: upload.ID, WorkspaceID: upload.WorkspaceID, Filename: upload.Filename, ContentType: upload.ContentType,
		ObjectKey: upload.ObjectKey, FieldKey: upload.FieldKey, SHA256: upload.SHA256, Size: upload.Size,
		Status: lifecycleScanStatus(value.ScanStatus), Provider: metadata.ScanProvider, EvidenceRef: metadata.ScanEvidenceRef, ScannedAt: scannedAt,
	}, nil
}

func sharedScanStatus(value string) (sharedartifact.ScanStatus, error) {
	switch strings.TrimSpace(value) {
	case lifecyclecontract.FileScanPending:
		return sharedartifact.ScanPending, nil
	case lifecyclecontract.FileScanClean:
		return sharedartifact.ScanClean, nil
	case lifecyclecontract.FileScanQuarantined:
		return sharedartifact.ScanRejected, nil
	case lifecyclecontract.FileScanFailed:
		return sharedartifact.ScanFailed, nil
	default:
		return "", fmt.Errorf("file scan status is invalid")
	}
}

func lifecycleScanStatus(value sharedartifact.ScanStatus) string {
	switch value {
	case sharedartifact.ScanClean:
		return lifecyclecontract.FileScanClean
	case sharedartifact.ScanRejected:
		return lifecyclecontract.FileScanQuarantined
	case sharedartifact.ScanFailed:
		return lifecyclecontract.FileScanFailed
	default:
		return lifecyclecontract.FileScanPending
	}
}

func encodeUploadArtifactMetadata(value uploadArtifactMetadata) (json.RawMessage, error) {
	return json.Marshal(value)
}

func decodeUploadArtifactMetadata(raw json.RawMessage) (uploadArtifactMetadata, error) {
	var value uploadArtifactMetadata
	if err := json.Unmarshal(raw, &value); err != nil {
		return value, fmt.Errorf("decode upload Artifact metadata: %w", err)
	}
	value.ObjectKey, value.FieldKey, value.ReferenceState = strings.TrimSpace(value.ObjectKey), strings.TrimSpace(value.FieldKey), strings.TrimSpace(value.ReferenceState)
	if value.ObjectKey == "" || value.FieldKey == "" {
		return value, fmt.Errorf("upload Artifact owner metadata is incomplete")
	}
	switch value.ReferenceState {
	case "staged", "referenced", "orphaned", "expired", "deleted":
	default:
		return value, fmt.Errorf("upload Artifact reference state is invalid")
	}
	return value, nil
}

var _ lifecyclecontract.UploadFileArtifactStore = (*FileArtifactStore)(nil)
var _ lifecyclecontract.PendingFileScanStore = (*FileArtifactStore)(nil)
