package lifecycle

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	sharedartifact "github.com/domainry/domainry-foundation/artifact"
	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	"github.com/domainry/domainry-lifecycle-sdk/modulehost"
	lifecyclemodel "github.com/domainry/domainry-lifecycle/internal/domain/lifecycle/model"
	artifactfixture "github.com/domainry/domainry-lifecycle/internal/testsupport/artifactfixture"
)

func TestUploadCleanupTransitionsTerminalBeforeDeleteAndRetries(t *testing.T) {
	artifacts := artifactfixture.NewStore()
	content := artifactfixture.NewContent()
	observer := &uploadCleanupObservedContent{delegate: content, artifacts: artifacts, deleteErr: errors.New("temporary Blob delete failure")}
	store := NewFileArtifactStore(
		persistenceTestHost{artifacts: artifacts, content: content},
		persistenceUploadFields{}, "", WithArtifactContentStore(observer),
	)
	createdAt := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	raw := []byte("upload content")
	info, err := content.PutImmutable(t.Context(), "workspace-a", "upload.bin", raw)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.RegisterUpload(t.Context(), lifecyclecontract.UploadArtifact{
		ID: "upload-a", WorkspaceID: "workspace-a", ObjectKey: "document", FieldKey: "file",
		Filename: info.Reference, ContentType: "application/octet-stream", SHA256: info.SHA256, Size: info.Size, CreatedAt: createdAt,
	}); err != nil {
		t.Fatal(err)
	}
	scope := lifecycleaccess.NewSystemScope(lifecycleaccess.SystemScopeGlobal, "reconcile upload artifacts")
	now := createdAt.Add(uploadArtifactGracePeriod + time.Minute)
	if _, err = store.ReconcileUploadArtifacts(t.Context(), scope, now, 10); !errors.Is(err, observer.deleteErr) {
		t.Fatalf("first cleanup err=%v", err)
	}
	persisted, found, err := artifacts.ByID(t.Context(), "workspace-a", "upload-a")
	if err != nil || !found || persisted.Status != sharedartifact.StatusExpired || len(observer.statuses) != 1 || observer.statuses[0] != sharedartifact.StatusExpired {
		t.Fatalf("failed cleanup artifact=%#v found=%v delete statuses=%v err=%v", persisted, found, observer.statuses, err)
	}
	observer.deleteErr = nil
	result, err := store.ReconcileUploadArtifacts(t.Context(), scope, now.Add(time.Minute), 10)
	if err != nil || result.Deleted != 1 {
		t.Fatalf("retry result=%#v err=%v", result, err)
	}
	persisted, found, err = artifacts.ByID(t.Context(), "workspace-a", "upload-a")
	if err != nil || !found || persisted.Status != sharedartifact.StatusDeleted {
		t.Fatalf("deleted artifact=%#v found=%v err=%v", persisted, found, err)
	}
	if _, err = content.Stat(t.Context(), "workspace-a", info.Reference); !errors.Is(err, lifecyclecontract.ErrArtifactContentNotFound) {
		t.Fatalf("content remains: %v", err)
	}
	result, err = store.ReconcileUploadArtifacts(t.Context(), scope, now.Add(2*time.Minute), 10)
	if err != nil || result.Deleted != 0 {
		t.Fatalf("idempotent replay result=%#v err=%v", result, err)
	}
}

func TestSubjectFileErasureCommitsTerminalArtifactBeforeBlobDelete(t *testing.T) {
	artifacts := artifactfixture.NewStore()
	content := artifactfixture.NewContent()
	observer := &uploadCleanupObservedContent{delegate: content, artifacts: artifacts, deleteErr: errors.New("temporary subject Blob delete failure")}
	host := subjectArtifactCleanupHost{artifacts: artifacts, content: content}
	register := NewFileArtifactStore(host, persistenceUploadFields{}, "", WithArtifactContentStore(observer))
	createdAt := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	raw := []byte("subject upload content")
	info, err := content.PutImmutable(t.Context(), "workspace-a", "subject.pdf", raw)
	if err != nil {
		t.Fatal(err)
	}
	if err = register.RegisterUpload(t.Context(), lifecyclecontract.UploadArtifact{
		ID: "subject-upload", WorkspaceID: "workspace-a", ObjectKey: "document", FieldKey: "file",
		Filename: info.Reference, ContentType: "application/pdf", SHA256: info.SHA256, Size: info.Size, CreatedAt: createdAt,
	}); err != nil {
		t.Fatal(err)
	}
	files := NewSubjectArtifactStore(host, "", observer)
	reference := lifecyclecontract.SubjectFileReference{WorkspaceID: "workspace-a", Reference: "/uploads/subject.pdf"}
	expected, err := files.ExportSubjectFile(t.Context(), reference)
	if err != nil {
		t.Fatal(err)
	}
	expected.Content = nil
	if _, err = files.DeleteSubjectFileVersion(t.Context(), reference, expected); !errors.Is(err, observer.deleteErr) {
		t.Fatalf("first erasure err=%v", err)
	}
	persisted, found, err := artifacts.ByID(t.Context(), "workspace-a", "subject-upload")
	if err != nil || !found || persisted.Status != sharedartifact.StatusExpired || observer.statuses[0] != sharedartifact.StatusExpired {
		t.Fatalf("failed erasure artifact=%#v found=%v delete statuses=%v err=%v", persisted, found, observer.statuses, err)
	}
	observer.deleteErr = nil
	if _, err = files.DeleteSubjectFileVersion(t.Context(), reference, expected); err != nil {
		t.Fatal(err)
	}
	persisted, found, err = artifacts.ByID(t.Context(), "workspace-a", "subject-upload")
	if err != nil || !found || persisted.Status != sharedartifact.StatusDeleted {
		t.Fatalf("erased artifact=%#v found=%v err=%v", persisted, found, err)
	}
}

type subjectArtifactCleanupHost struct {
	artifacts sharedartifact.ManagedStore
	content   *artifactfixture.Content
}

func TestLifecycleArchiveRegistrationFailureDeletesUnreferencedBlob(t *testing.T) {
	base := artifactfixture.NewStore()
	content := artifactfixture.NewContent()
	registerErr := errors.New("archive registration failed")
	host := subjectArtifactCleanupHost{artifacts: &failingLifecycleArtifactStore{ManagedStore: base, err: registerErr}, content: content}
	writer := NewArchiveWriter(host)
	job := lifecyclemodel.CleanupJob{ID: "job-a", WorkspaceID: "workspace-a", RequestedBy: "operator-a", UpdatedAt: time.Date(2026, 9, 23, 17, 0, 0, 0, time.UTC)}
	policy := lifecyclemodel.PolicyVersion{Policy: lifecyclemodel.RetentionPolicy{Key: "records", Version: "1"}}
	if _, err := writer.ArchivePayload(t.Context(), "record", job, policy, "records", "record-a", []byte(`{"id":"record-a"}`)); !errors.Is(err, registerErr) {
		t.Fatalf("archive err=%v", err)
	}
	artifactID := archiveArtifactID(job.WorkspaceID, "records", "record-a", policy.Policy.Key)
	if _, err := content.Stat(t.Context(), job.WorkspaceID, artifactID); !errors.Is(err, lifecyclecontract.ErrArtifactContentNotFound) {
		t.Fatalf("unregistered archive content remains: %v", err)
	}
}

type failingLifecycleArtifactStore struct {
	sharedartifact.ManagedStore
	err error
}

func (s *failingLifecycleArtifactStore) Register(context.Context, sharedartifact.Artifact) (sharedartifact.Artifact, bool, error) {
	return sharedartifact.Artifact{}, false, s.err
}

func (subjectArtifactCleanupHost) Database() modulehost.Database             { return nil }
func (subjectArtifactCleanupHost) Dialect() modulehost.Dialect               { return nil }
func (subjectArtifactCleanupHost) Migrations() modulehost.MigrationRegistrar { return nil }
func (subjectArtifactCleanupHost) Transactions() modulehost.Transactor {
	return directLifecycleTransactor{}
}
func (h subjectArtifactCleanupHost) ArtifactStore() sharedartifact.ManagedStore { return h.artifacts }
func (h subjectArtifactCleanupHost) ArtifactContentStore() lifecyclecontract.ArtifactContentStore {
	return h.content
}
func (h subjectArtifactCleanupHost) ArtifactContentWriter() lifecyclecontract.ArtifactContentWriter {
	return h.content
}

type directLifecycleTransactor struct{}

func (directLifecycleTransactor) WithinTransaction(ctx context.Context, operation func(context.Context, modulehost.DBTX) error) error {
	return operation(ctx, nil)
}

type uploadCleanupObservedContent struct {
	delegate  lifecyclecontract.ArtifactContentStore
	artifacts sharedartifact.ManagedStore
	deleteErr error
	statuses  []sharedartifact.Status
}

func (s *uploadCleanupObservedContent) Open(ctx context.Context, workspaceID, reference string) (io.ReadCloser, error) {
	return s.delegate.Open(ctx, workspaceID, reference)
}

func (s *uploadCleanupObservedContent) Stat(ctx context.Context, workspaceID, reference string) (sharedartifact.ContentInfo, error) {
	return s.delegate.Stat(ctx, workspaceID, reference)
}

func (s *uploadCleanupObservedContent) Delete(ctx context.Context, workspaceID, reference string) error {
	values, err := s.artifacts.List(ctx, workspaceID, sharedartifact.Query{Owner: sharedartifact.OwnerUploads, Kind: "file", StorageReference: reference, Limit: 1})
	if err != nil {
		return err
	}
	if len(values) == 1 {
		s.statuses = append(s.statuses, values[0].Status)
	}
	if s.deleteErr != nil {
		return s.deleteErr
	}
	return s.delegate.Delete(ctx, workspaceID, reference)
}
