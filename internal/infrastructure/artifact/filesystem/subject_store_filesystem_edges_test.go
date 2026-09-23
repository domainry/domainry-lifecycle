package filesystem

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
)

func TestLifecycleUploadStagingFilesystemFailureEdges(t *testing.T) {
	originalReadDir, originalInfo := localReadDir, localDirEntryInfo
	originalRemove, originalAbs := localRemove, localAbsPath
	t.Cleanup(func() {
		localReadDir, localDirEntryInfo = originalReadDir, originalInfo
		localRemove, localAbsPath = originalRemove, originalAbs
	})
	reset := func() {
		localReadDir, localDirEntryInfo = originalReadDir, originalInfo
		localRemove, localAbsPath = originalRemove, originalAbs
	}
	store := NewSubjectStore(t.TempDir())
	now := time.Now().UTC()
	localAbsPath = func(string) (string, error) { return "", errors.New("abs") }
	if _, err := store.DeleteExpiredUploadStaging(t.Context(), now); err == nil {
		t.Fatal("staging abs failure ignored")
	}
	reset()
	missingRootStore := NewSubjectStore(filepath.Join(t.TempDir(), "missing"))
	if deleted, err := missingRootStore.DeleteExpiredUploadStaging(t.Context(), now); err != nil || deleted != 0 {
		t.Fatalf("missing staging root deleted=%d err=%v", deleted, err)
	}
	localReadDir = func(string) ([]os.DirEntry, error) { return nil, errors.New("read-dir") }
	if _, err := store.DeleteExpiredUploadStaging(t.Context(), now); err == nil {
		t.Fatal("staging read-dir failure ignored")
	}
	reset()
	workspace := filepath.Join(store.uploadRoot, "workspace-test")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.uploadRoot, "workspace-file"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(workspace, ".upload-directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	stagePath := filepath.Join(workspace, ".upload-old")
	if err := os.WriteFile(stagePath, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := now.Add(-2 * time.Hour)
	if err := os.Chtimes(stagePath, old, old); err != nil {
		t.Fatal(err)
	}
	localReadDir = func(path string) ([]os.DirEntry, error) {
		if path == workspace {
			return nil, errors.New("workspace read")
		}
		return originalReadDir(path)
	}
	if _, err := store.DeleteExpiredUploadStaging(t.Context(), now); err == nil {
		t.Fatal("workspace read failure ignored")
	}
	reset()
	localDirEntryInfo = func(os.DirEntry) (os.FileInfo, error) { return nil, errors.New("info") }
	if _, err := store.DeleteExpiredUploadStaging(t.Context(), now); err == nil {
		t.Fatal("staging info failure ignored")
	}
	reset()
	localRemove = func(path string) error {
		if path == stagePath {
			return errors.New("remove")
		}
		return originalRemove(path)
	}
	if _, err := store.DeleteExpiredUploadStaging(t.Context(), now); err == nil {
		t.Fatal("staging remove failure ignored")
	}
}

func TestLifecycleSubjectFileFilesystemFailureEdges(t *testing.T) {
	originalOpen, originalRead, originalRemove, originalAbs := localOpenFile, localReadFile, localRemove, localAbsPath
	t.Cleanup(func() {
		localOpenFile, localReadFile, localRemove, localAbsPath = originalOpen, originalRead, originalRemove, originalAbs
	})
	root := t.TempDir()
	store := NewSubjectStore(root)
	reference := lifecyclecontract.SubjectFileReference{WorkspaceID: "workspace-a", Reference: "/uploads/file.txt"}
	if _, err := store.ExportSubjectFile(t.Context(), lifecyclecontract.SubjectFileReference{}); err == nil {
		t.Fatal("invalid subject export accepted")
	}
	if _, _, err := NewSubjectStore("").subjectFilePath(reference); err == nil {
		t.Fatal("empty upload root accepted")
	}
	localOpenFile = func(string) (localReadableFile, error) { return &failingReadableFile{statErr: errors.New("stat")}, nil }
	if _, err := store.ExportSubjectFile(t.Context(), reference); err == nil {
		t.Fatal("stat failure ignored")
	}
	localOpenFile = func(string) (localReadableFile, error) { return &failingReadableFile{info: fakeFileInfo{size: 1}}, nil }
	localReadFile = func(string) ([]byte, error) { return nil, errors.New("read") }
	if _, err := store.ExportSubjectFile(t.Context(), reference); err == nil {
		t.Fatal("read failure ignored")
	}
	localOpenFile, localReadFile = originalOpen, originalRead
	path, _, err := store.subjectFilePath(reference)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	localRemove = func(string) error { return errors.New("remove") }
	if _, err := store.DeleteSubjectFile(t.Context(), reference); err == nil {
		t.Fatal("subject remove failure ignored")
	}
	if _, err := store.DeleteSubjectFile(t.Context(), lifecyclecontract.SubjectFileReference{}); err == nil {
		t.Fatal("invalid subject deletion accepted")
	}
	localAbsPath = func(string) (string, error) { return "", errors.New("abs") }
	if _, _, err := store.subjectFilePath(reference); err == nil {
		t.Fatal("subject abs failure ignored")
	}
	localAbsPath = originalAbs
	if _, _, err := store.subjectFilePath(lifecyclecontract.SubjectFileReference{WorkspaceID: "workspace-a", Reference: "%"}); err == nil {
		t.Fatal("invalid subject URL accepted")
	}
	if _, _, err := store.subjectFilePath(lifecyclecontract.SubjectFileReference{WorkspaceID: "workspace-a", Reference: ".."}); err == nil {
		t.Fatal("parent filename accepted")
	}
}

type failingReadableFile struct {
	info    os.FileInfo
	statErr error
}

func (file *failingReadableFile) Stat() (os.FileInfo, error) { return file.info, file.statErr }
func (*failingReadableFile) Close() error                    { return nil }

type fakeFileInfo struct{ size int64 }

func (fakeFileInfo) Name() string       { return "file" }
func (info fakeFileInfo) Size() int64   { return info.size }
func (fakeFileInfo) Mode() os.FileMode  { return 0 }
func (fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (fakeFileInfo) IsDir() bool        { return false }
func (fakeFileInfo) Sys() any           { return nil }

var _ fs.FileInfo = fakeFileInfo{}
