package filesystem

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
)

func TestUploadStagingAndSubjectFileFailureEdges(t *testing.T) {
	if deleted, err := NewSubjectStore(t.TempDir()).DeleteExpiredUploadStaging(t.Context(), time.Now()); err != nil || deleted != 0 {
		t.Fatalf("empty staging deleted=%d err=%v", deleted, err)
	}
	if _, err := NewSubjectStore("").DeleteExpiredUploadStaging(t.Context(), time.Now()); err == nil {
		t.Fatal("empty upload root accepted")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "not-workspace"), []byte("ignored"), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(root, "workspace-test")
	if err := os.MkdirAll(filepath.Join(workspace, ".upload-directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "final.txt"), []byte("final"), 0o600); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := NewSubjectStore(root).DeleteExpiredUploadStaging(cancelled, time.Now()); !errors.Is(err, context.Canceled) {
		t.Fatalf("staging cancellation=%v", err)
	}

	store := NewSubjectStore(root)
	for _, reference := range []lifecyclecontract.SubjectFileReference{
		{WorkspaceID: "", Reference: "/uploads/file.txt"},
		{WorkspaceID: "workspace-a", Reference: ""},
		{WorkspaceID: "workspace-a", Reference: "/"},
	} {
		if _, _, err := store.subjectFilePath(reference); err == nil {
			t.Fatalf("invalid subject reference=%+v accepted", reference)
		}
	}
	if _, err := store.ExportSubjectFile(cancelled, lifecyclecontract.SubjectFileReference{WorkspaceID: "workspace-a", Reference: "/uploads/file.txt"}); !errors.Is(err, context.Canceled) && !os.IsNotExist(err) {
		t.Fatalf("export cancellation/path error=%v", err)
	}
	workspaceID := "workspace-a"
	digest := sha256.Sum256([]byte(workspaceID))
	directory := filepath.Join(root, "workspace-"+hex.EncodeToString(digest[:16]))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	large := filepath.Join(directory, "large.bin")
	file, err := os.Create(large)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(lifecycleSubjectFileLimit + 1); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	reference := lifecyclecontract.SubjectFileReference{WorkspaceID: workspaceID, Reference: "/uploads/large.bin"}
	if _, err := store.ExportSubjectFile(t.Context(), reference); err == nil {
		t.Fatal("oversized subject file exported")
	}
	missing := lifecyclecontract.SubjectFileReference{WorkspaceID: workspaceID, Reference: "/uploads/missing.txt"}
	if _, err := store.DeleteSubjectFile(t.Context(), missing); err == nil {
		t.Fatal("missing subject file deleted")
	}
}
