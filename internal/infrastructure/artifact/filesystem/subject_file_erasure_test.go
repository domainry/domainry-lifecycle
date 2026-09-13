package filesystem

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
)

func TestPreparedSubjectFileErasureRetainsChangedFilesAndReplaysAbsentFile(t *testing.T) {
	root := t.TempDir()
	digest := sha256.Sum256([]byte("workspace-one"))
	directory := filepath.Join(root, "workspace-"+hex.EncodeToString(digest[:16]))
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "file.txt")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	store := NewSubjectStore(root)
	reference := lifecyclecontract.SubjectFileReference{WorkspaceID: "workspace-one", ObjectKey: "profile", RecordID: "one", FieldKey: "file", Reference: "/uploads/file.txt"}
	prepared, err := store.ExportSubjectFile(t.Context(), reference)
	if err != nil {
		t.Fatal(err)
	}
	prepared.Content = nil
	if err := os.WriteFile(path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DeleteSubjectFileVersion(t.Context(), reference, prepared); err == nil {
		t.Fatal("changed file was accepted")
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != "changed" {
		t.Fatalf("changed file lost: %s %v", raw, err)
	}
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	first, err := store.DeleteSubjectFileVersion(t.Context(), reference, prepared)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := store.DeleteSubjectFileVersion(t.Context(), reference, prepared)
	if err != nil || !reflect.DeepEqual(first, replayed) {
		t.Fatalf("retry receipt changed: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file remains: %v", err)
	}
	prepared.SHA256 = ""
	if _, err := store.DeleteSubjectFileVersion(t.Context(), reference, prepared); err == nil {
		t.Fatal("missing evidence was accepted for absent file")
	}
}
