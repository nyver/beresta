package backup

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	ManifestVersion      = 1
	MaxManifestEntries   = 1_000_000
	MaxManifestPathBytes = 4096
)

var (
	ErrInvalidManifest      = errors.New("backup: invalid manifest")
	ErrUnsafeManifestPath   = errors.New("backup: unsafe manifest path")
	ErrManifestVerification = errors.New("backup: manifest verification failed")
)

type Manifest struct {
	Version uint32          `json:"version" cbor:"version"`
	Entries []ManifestEntry `json:"entries" cbor:"entries"`
}

type ManifestEntry struct {
	Path   string `json:"path" cbor:"path"`
	Size   uint64 `json:"size" cbor:"size"`
	SHA256 []byte `json:"sha256" cbor:"sha256"`
}

// GenerateManifest hashes a caller-selected set of regular files beneath
// root. Paths are normalized forward-slash relative names in canonical order.
func GenerateManifest(ctx context.Context, root string, relativePaths []string) (Manifest, error) {
	if len(relativePaths) == 0 || len(relativePaths) > MaxManifestEntries {
		return Manifest{}, ErrInvalidManifest
	}
	resolvedRoot, err := resolveRoot(root)
	if err != nil {
		return Manifest{}, fmt.Errorf("backup: resolve manifest root: %w", err)
	}
	paths := append([]string(nil), relativePaths...)
	sort.Strings(paths)
	manifest := Manifest{Version: ManifestVersion, Entries: make([]ManifestEntry, 0, len(paths))}
	previous := ""
	portablePaths := make(map[string]struct{}, len(paths))
	for _, relative := range paths {
		if err := contextError(ctx); err != nil {
			return Manifest{}, err
		}
		if err := validateRelativePath(relative); err != nil {
			return Manifest{}, err
		}
		if previous != "" && relative == previous {
			return Manifest{}, ErrInvalidManifest
		}
		portable := strings.ToLower(relative)
		if _, exists := portablePaths[portable]; exists {
			return Manifest{}, ErrInvalidManifest
		}
		portablePaths[portable] = struct{}{}
		previous = relative
		entry, err := hashEntry(ctx, resolvedRoot, relative)
		if err != nil {
			return Manifest{}, err
		}
		manifest.Entries = append(manifest.Entries, entry)
	}
	return manifest, nil
}

// VerifyManifest re-hashes every declared file and rejects missing, replaced,
// linked, special, size-mismatched, or content-mismatched entries.
func VerifyManifest(ctx context.Context, root string, manifest Manifest) error {
	if err := manifest.Validate(); err != nil {
		return err
	}
	resolvedRoot, err := resolveRoot(root)
	if err != nil {
		return fmt.Errorf("backup: resolve manifest root: %w", err)
	}
	for _, expected := range manifest.Entries {
		if err := contextError(ctx); err != nil {
			return err
		}
		actual, err := hashEntry(ctx, resolvedRoot, expected.Path)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrManifestVerification, err)
		}
		if actual.Size != expected.Size || !equalBytes(actual.SHA256, expected.SHA256) {
			return ErrManifestVerification
		}
	}
	return nil
}

func (manifest Manifest) Validate() error {
	if manifest.Version != ManifestVersion || len(manifest.Entries) == 0 || len(manifest.Entries) > MaxManifestEntries {
		return ErrInvalidManifest
	}
	previous := ""
	portablePaths := make(map[string]struct{}, len(manifest.Entries))
	for index, entry := range manifest.Entries {
		if err := validateRelativePath(entry.Path); err != nil {
			return err
		}
		if len(entry.SHA256) != sha256.Size {
			return ErrInvalidManifest
		}
		if index > 0 && entry.Path <= previous {
			return ErrInvalidManifest
		}
		portable := strings.ToLower(entry.Path)
		if _, exists := portablePaths[portable]; exists {
			return ErrInvalidManifest
		}
		portablePaths[portable] = struct{}{}
		previous = entry.Path
	}
	return nil
}

func hashEntry(ctx context.Context, root, relative string) (ManifestEntry, error) {
	if err := validateRelativePath(relative); err != nil {
		return ManifestEntry{}, err
	}
	target := filepath.Join(root, filepath.FromSlash(relative))
	if err := rejectSymlinkComponents(root, relative); err != nil {
		return ManifestEntry{}, fmt.Errorf("backup: inspect %q: %w", relative, err)
	}
	relativeCheck, err := filepath.Rel(root, target)
	if err != nil || relativeCheck == ".." || strings.HasPrefix(relativeCheck, ".."+string(filepath.Separator)) {
		return ManifestEntry{}, ErrUnsafeManifestPath
	}
	before, err := os.Lstat(target)
	if err != nil {
		return ManifestEntry{}, fmt.Errorf("backup: inspect %q: %w", relative, err)
	}
	if !before.Mode().IsRegular() || before.Mode()&fs.ModeSymlink != 0 {
		return ManifestEntry{}, ErrUnsafeManifestPath
	}
	file, err := os.Open(target)
	if err != nil {
		return ManifestEntry{}, fmt.Errorf("backup: open %q: %w", relative, err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return ManifestEntry{}, fmt.Errorf("backup: inspect opened %q: %w", relative, err)
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return ManifestEntry{}, ErrUnsafeManifestPath
	}

	digest := sha256.New()
	buffer := make([]byte, 128*1024)
	var size uint64
	for {
		if err := contextError(ctx); err != nil {
			clear(buffer)
			return ManifestEntry{}, err
		}
		read, readErr := file.Read(buffer)
		if read > 0 {
			size += uint64(read)
			_, _ = digest.Write(buffer[:read])
			clear(buffer[:read])
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			clear(buffer)
			return ManifestEntry{}, readErr
		}
		if read == 0 {
			clear(buffer)
			return ManifestEntry{}, io.ErrNoProgress
		}
	}
	clear(buffer)
	after, err := file.Stat()
	if err != nil {
		return ManifestEntry{}, fmt.Errorf("backup: re-inspect %q: %w", relative, err)
	}
	if !os.SameFile(opened, after) || after.Size() < 0 || uint64(after.Size()) != size || after.ModTime() != opened.ModTime() {
		return ManifestEntry{}, ErrManifestVerification
	}
	return ManifestEntry{Path: relative, Size: size, SHA256: digest.Sum(nil)}, nil
}

func resolveRoot(root string) (string, error) {
	if root == "" {
		return "", ErrUnsafeManifestPath
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	resolved := filepath.Clean(absolute)
	info, err := os.Lstat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
		return "", ErrUnsafeManifestPath
	}
	return resolved, nil
}

func validateRelativePath(value string) error {
	if value == "" || value == "." || len(value) > MaxManifestPathBytes || !utf8.ValidString(value) {
		return ErrUnsafeManifestPath
	}
	if strings.Contains(value, "\\") || strings.ContainsRune(value, '\x00') || path.IsAbs(value) || filepath.IsAbs(value) || filepath.VolumeName(value) != "" || hasDrivePrefix(value) {
		return ErrUnsafeManifestPath
	}
	if !fs.ValidPath(value) || path.Clean(value) != value {
		return ErrUnsafeManifestPath
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return ErrUnsafeManifestPath
		}
	}
	return nil
}

func rejectSymlinkComponents(root, relative string) error {
	current := root
	parts := strings.Split(relative, "/")
	for index, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return ErrUnsafeManifestPath
		}
		if index < len(parts)-1 && !info.IsDir() {
			return ErrUnsafeManifestPath
		}
	}
	return nil
}

func hasDrivePrefix(value string) bool {
	if len(value) < 2 || value[1] != ':' {
		return false
	}
	first := value[0]
	return first >= 'A' && first <= 'Z' || first >= 'a' && first <= 'z'
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var different byte
	for index := range left {
		different |= left[index] ^ right[index]
	}
	return different == 0
}
