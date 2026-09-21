package filesystem

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"testing"

	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
)

type subjectContentStoreStub struct{ content map[string][]byte }

func (s *subjectContentStoreStub) Open(_ context.Context, workspaceID, key string) (io.ReadCloser, error) {
	content, ok := s.content[workspaceID+"\x00"+key]
	if !ok {
		return nil, lifecyclecontract.ErrArtifactContentNotFound
	}
	return io.NopCloser(bytes.NewReader(content)), nil
}

func (s *subjectContentStoreStub) Stat(_ context.Context, workspaceID, key string) (lifecyclecontract.ArtifactContentInfo, error) {
	content, ok := s.content[workspaceID+"\x00"+key]
	if !ok {
		return lifecyclecontract.ArtifactContentInfo{}, lifecyclecontract.ErrArtifactContentNotFound
	}
	digest := sha256.Sum256(content)
	return lifecyclecontract.ArtifactContentInfo{SHA256: hex.EncodeToString(digest[:]), Size: int64(len(content))}, nil
}

func (s *subjectContentStoreStub) Delete(_ context.Context, workspaceID, key string) error {
	delete(s.content, workspaceID+"\x00"+key)
	return nil
}

func TestSubjectStoreUsesDeploymentContentStoreForExportAndExactErasure(t *testing.T) {
	content := []byte("remote artifact bytes")
	storage := &subjectContentStoreStub{content: map[string][]byte{"workspace-a\x00document.pdf": content}}
	store := NewSubjectStore(t.TempDir(), storage)
	reference := lifecyclecontract.SubjectFileReference{WorkspaceID: "workspace-a", Reference: "/uploads/document.pdf"}
	prepared, err := store.ExportSubjectFile(t.Context(), reference)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(prepared.Content, content) || prepared.Filename != "document.pdf" {
		t.Fatalf("prepared=%+v", prepared)
	}
	prepared.Content = nil
	if _, err := store.DeleteSubjectFileVersion(t.Context(), reference, prepared); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.Stat(t.Context(), "workspace-a", "document.pdf"); !errors.Is(err, lifecyclecontract.ErrArtifactContentNotFound) {
		t.Fatalf("content remains: %v", err)
	}
	if _, err := store.DeleteSubjectFileVersion(t.Context(), reference, prepared); err != nil {
		t.Fatalf("idempotent replay failed: %v", err)
	}
}

func TestSubjectStoreRejectsChangedDeploymentContentDuringErasure(t *testing.T) {
	storage := &subjectContentStoreStub{content: map[string][]byte{"workspace-a\x00document.pdf": []byte("first")}}
	store := NewSubjectStore(t.TempDir(), storage)
	reference := lifecyclecontract.SubjectFileReference{WorkspaceID: "workspace-a", Reference: "/uploads/document.pdf"}
	prepared, err := store.ExportSubjectFile(t.Context(), reference)
	if err != nil {
		t.Fatal(err)
	}
	prepared.Content = nil
	storage.content["workspace-a\x00document.pdf"] = []byte("changed")
	if _, err := store.DeleteSubjectFileVersion(t.Context(), reference, prepared); err == nil {
		t.Fatal("changed content was erased using stale evidence")
	}
}
