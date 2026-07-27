package mcptools

// save_attachment's confined local write: the mirror of attachment_source.go's
// content_path reads.

import (
	"crypto/rand"
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

	if err := scope.writeExclusive(data); err != nil {
		return "", fmt.Errorf("output_path %q: %w", path, err)
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

// writeScope is the filesystem view every step of the write runs through: an
// os.Root when roots confine it, the plain filesystem otherwise. Sharing one
// scope keeps the temporary file, the link, and the cleanup inside the same
// confinement, so none of them can follow a swapped path component out of the
// allowed directory.
type writeScope struct {
	root *os.Root // nil when no roots are configured
	name string   // path within root, or the absolute path when root is nil
}

// writeExclusive fills a temporary sibling and only then links it to the
// caller's name, so that name appears exactly once, already complete. Failure
// cleanup therefore only ever touches the unpredictable temporary name: no
// check-then-delete of a path a caller or another process may own. Identity
// comparison cannot substitute here, because a filesystem that reuses inodes
// makes a freshly created replacement indistinguishable from the original.
func (s writeScope) writeExclusive(data []byte) error {
	tmp, err := s.tempName()
	if err != nil {
		return err
	}
	f, err := s.openFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return err
	}
	if err := writeAndClose(f, data); err != nil {
		_ = s.remove(tmp)
		return err
	}
	// Link refuses an existing destination, which is the never-overwrite
	// guarantee, and publishes the complete file in one step.
	if err := s.link(tmp, s.name); err != nil {
		_ = s.remove(tmp)
		return err
	}
	_ = s.remove(tmp)
	return nil
}

// tempName is a sibling of the target, so the eventual link stays within one
// directory and therefore one filesystem.
func (s writeScope) tempName() (string, error) {
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", fmt.Errorf("naming temporary file: %w", err)
	}
	return fmt.Sprintf("%s.partial-%x", s.name, suffix), nil
}

func (s writeScope) openFile(name string, flags int) (*os.File, error) {
	if s.root == nil {
		return os.OpenFile(name, flags, 0o600)
	}
	return s.root.OpenFile(name, flags, 0o600)
}

func (s writeScope) remove(name string) error {
	if s.root == nil {
		return os.Remove(name)
	}
	return s.root.Remove(name)
}

func (s writeScope) link(oldname, newname string) error {
	if s.root == nil {
		return os.Link(oldname, newname)
	}
	return s.root.Link(oldname, newname)
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
