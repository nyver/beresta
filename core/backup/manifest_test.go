package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateAndVerifyManifest(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "blobs", "aa"), 0o700); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"profile.db":      []byte("encrypted sqlite snapshot"),
		"blobs/aa/object": bytes.Repeat([]byte{0x42}, 4097),
	}
	for relative, content := range files {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(relative)), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	manifest, err := GenerateManifest(context.Background(), root, []string{"profile.db", "blobs/aa/object"})
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Entries) != 2 || manifest.Entries[0].Path != "blobs/aa/object" || manifest.Entries[1].Path != "profile.db" {
		t.Fatalf("manifest order = %+v", manifest.Entries)
	}
	for _, entry := range manifest.Entries {
		want := sha256.Sum256(files[entry.Path])
		if entry.Size != uint64(len(files[entry.Path])) || !bytes.Equal(entry.SHA256, want[:]) {
			t.Fatalf("entry %q = %+v", entry.Path, entry)
		}
	}
	if err := VerifyManifest(context.Background(), root, manifest); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(root, "profile.db"), []byte("tampered sqlite snapshot"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyManifest(context.Background(), root, manifest); !errors.Is(err, ErrManifestVerification) {
		t.Fatalf("tampered verification error = %v", err)
	}
}

func TestManifestRejectsUnsafeAndNonCanonicalPaths(t *testing.T) {
	root := t.TempDir()
	unsafePaths := []string{
		"",
		".",
		"../profile.db",
		"sub/../profile.db",
		"/absolute",
		"C:/drive/path",
		`sub\windows-path`,
		"sub//empty",
		"line\nbreak",
	}
	if _, err := GenerateManifest(context.Background(), root, nil); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("empty manifest error = %v", err)
	}
	for _, unsafe := range unsafePaths {
		if _, err := GenerateManifest(context.Background(), root, []string{unsafe}); !errors.Is(err, ErrUnsafeManifestPath) {
			t.Fatalf("path %q error = %v", unsafe, err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "duplicate"), []byte("value"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := GenerateManifest(context.Background(), root, []string{"duplicate", "duplicate"}); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("duplicate path error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "DUPLICATE"), []byte("value"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := GenerateManifest(context.Background(), root, []string{"DUPLICATE", "duplicate"}); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("portable case-fold collision error = %v", err)
	}

	unsorted := Manifest{Version: ManifestVersion, Entries: []ManifestEntry{
		{Path: "z", SHA256: make([]byte, sha256.Size)},
		{Path: "a", SHA256: make([]byte, sha256.Size)},
	}}
	if err := unsorted.Validate(); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("unsorted manifest error = %v", err)
	}
	badHash := Manifest{Version: ManifestVersion, Entries: []ManifestEntry{{Path: "a", SHA256: make([]byte, sha256.Size-1)}}}
	if err := badHash.Validate(); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("short hash error = %v", err)
	}
}

func TestManifestRejectsDirectoriesLinksAndCancellation(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := GenerateManifest(context.Background(), root, []string{"directory"}); !errors.Is(err, ErrUnsafeManifestPath) {
		t.Fatalf("directory error = %v", err)
	}
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("value"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err == nil {
		if _, err := GenerateManifest(context.Background(), root, []string{"link"}); !errors.Is(err, ErrUnsafeManifestPath) {
			t.Fatalf("symlink error = %v", err)
		}
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := GenerateManifest(cancelled, root, []string{"target"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled generation error = %v", err)
	}
}

func TestManifestRejectsSymlinkRootWhenSupported(t *testing.T) {
	parent := t.TempDir()
	realRoot := filepath.Join(parent, "real")
	if err := os.Mkdir(realRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realRoot, "snapshot"), []byte("value"), 0o600); err != nil {
		t.Fatal(err)
	}
	linkedRoot := filepath.Join(parent, "linked")
	if err := os.Symlink(realRoot, linkedRoot); err != nil {
		t.Skipf("directory symlinks are unavailable: %v", err)
	}
	if _, err := GenerateManifest(context.Background(), linkedRoot, []string{"snapshot"}); !errors.Is(err, ErrUnsafeManifestPath) {
		t.Fatalf("symlink root error = %v", err)
	}
}
