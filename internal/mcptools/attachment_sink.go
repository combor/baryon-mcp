package mcptools

// save_attachment's confined local write: the mirror of attachment_source.go's
// content_path reads.

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// writeAttachmentFile creates path and writes data, confined to roots when they
// are set. It refuses to overwrite an existing file, so a stray path can never
// clobber data, and returns the symlink-resolved path actually written.
func writeAttachmentFile(path string, data []byte, roots attachmentRoots) (string, error) {
	// Fail closed on Windows for the same reason content_path does: resolving a
	// junction that targets \\host\share authenticates to the remote SMB host.
	if runtime.GOOS == "windows" {
		return "", fmt.Errorf("save_attachment is not supported on Windows; use get_attachment to read the attachment inline instead")
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("output_path %q is not an absolute path", path)
	}
	if len(path) >= 2 && os.IsPathSeparator(path[0]) && os.IsPathSeparator(path[1]) {
		return "", fmt.Errorf("output_path %q is a UNC or device path, which is not supported", path)
	}
	if os.IsPathSeparator(path[len(path)-1]) {
		return "", fmt.Errorf("output_path %q must name a file, not a directory", path)
	}
	base := filepath.Base(path)
	if base == "." || base == ".." {
		return "", fmt.Errorf("output_path %q does not name a file", path)
	}
	// The parent must already exist; we never create directories.
	resolvedDir, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", fmt.Errorf("output_path %q parent directory: %w", path, err)
	}
	if info, err := os.Stat(resolvedDir); err != nil || !info.IsDir() {
		return "", fmt.Errorf("output_path %q parent is not a directory", path)
	}

	scope, closeScope, err := openWriteScope(resolvedDir, base, roots)
	if err != nil {
		return "", fmt.Errorf("output_path %q: %w", path, err)
	}
	defer closeScope()

	f, err := scope.create()
	if err != nil {
		return "", fmt.Errorf("output_path %q: %w", path, err)
	}
	// Identify the file we created so cleanup can tell it from a replacement.
	created, statErr := f.Stat()
	if err := writeAndClose(f, data); err != nil {
		if statErr == nil {
			// Don't leave a truncated file a caller might mistake for the whole attachment.
			scope.removeIfUnchanged(created)
		}
		return "", fmt.Errorf("writing output_path %q: %w", path, err)
	}
	return filepath.Join(resolvedDir, base), nil
}

func writeAndClose(f *os.File, data []byte) error {
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// writeScope is the filesystem view used for the whole create-write-clean
// sequence: an os.Root when roots confine the write, the plain filesystem
// otherwise. Create and cleanup share one scope so a failed write is undone
// through the same confinement that made the file, never through a bare path
// that an ancestor symlink swap could redirect outside the allowed directory.
type writeScope struct {
	root *os.Root // nil when no roots are configured
	name string   // path within root, or the absolute path when root is nil
}

func (s writeScope) create() (*os.File, error) {
	const flags = os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if s.root == nil {
		return os.OpenFile(s.name, flags, 0o600)
	}
	return s.root.OpenFile(s.name, flags, 0o600)
}

// removeIfUnchanged deletes the file only while it is still the one created,
// so an entry another process swapped in is left alone rather than destroyed.
// POSIX has no unlink conditional on identity, so a swap landing between the
// stat and the unlink is still removed; closing that would mean never
// publishing the caller's path until the write succeeded, which costs more
// machinery than the residual window is worth here.
func (s writeScope) removeIfUnchanged(created os.FileInfo) {
	var (
		current os.FileInfo
		err     error
	)
	if s.root == nil {
		current, err = os.Lstat(s.name)
	} else {
		current, err = s.root.Lstat(s.name)
	}
	if err != nil || !os.SameFile(created, current) {
		return
	}
	if s.root == nil {
		_ = os.Remove(s.name)
		return
	}
	_ = s.root.Remove(s.name)
}

// openWriteScope resolves resolvedDir against roots. A configured boundary with
// no usable directory reaches the refusal below rather than allowing the write.
func openWriteScope(resolvedDir, base string, roots attachmentRoots) (writeScope, func(), error) {
	noop := func() {}
	if !roots.configured {
		return writeScope{name: filepath.Join(resolvedDir, base)}, noop, nil
	}
	root, rel, ok := roots.bind(resolvedDir, relDirWithinRoot)
	if !ok {
		return writeScope{}, noop, fmt.Errorf("file is outside the directories allowed by BARYON_ATTACHMENT_ROOTS")
	}
	return writeScope{root: root, name: filepath.Join(rel, base)}, func() { _ = root.Close() }, nil
}
