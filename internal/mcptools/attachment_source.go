package mcptools

// Draft attachment content sourcing: inline base64 and confined content_path reads.

import (
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/combor/baryon-mcp/internal/bridgeclient"
)

func decodeAttachment(index int, in draftAttachmentInput) (bridgeclient.DraftAttachment, error) {
	if len(*in.ContentBase64) > base64.StdEncoding.EncodedLen(bridgeclient.MaxDraftAttachmentBytes) {
		return bridgeclient.DraftAttachment{}, fmt.Errorf("attachment %d encoded content is above the %d byte decoded limit", index, bridgeclient.MaxDraftAttachmentBytes)
	}
	data, err := base64.StdEncoding.DecodeString(*in.ContentBase64)
	if err != nil {
		return bridgeclient.DraftAttachment{}, fmt.Errorf("attachment %d content_base64 is invalid: %w", index, err)
	}
	// EncodedLen is shared by several neighboring decoded sizes, so enforce
	// the exact per-attachment limit after decoding as well.
	if len(data) > bridgeclient.MaxDraftAttachmentBytes {
		return bridgeclient.DraftAttachment{}, fmt.Errorf("attachment %d decoded content is above the %d byte limit", index, bridgeclient.MaxDraftAttachmentBytes)
	}
	return bridgeclient.DraftAttachment{Filename: in.Filename, ContentType: in.ContentType, Data: data}, nil
}

// openAttachmentFile opens resolved for reading. With roots configured it opens
// through os.Root so a concurrent symlink swap cannot escape the allowed
// directory between validation and read. O_NONBLOCK keeps a file swapped for a
// FIFO from hanging the open; regular-file reads ignore it.
func openAttachmentFile(resolved string, roots attachmentRoots) (*os.File, error) {
	if !roots.configured {
		return os.OpenFile(resolved, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	}
	root, rel, ok := roots.bind(resolved, relWithinRoot)
	if !ok {
		return nil, fmt.Errorf("file is outside the directories allowed by BARYON_ATTACHMENT_ROOTS")
	}
	// The returned file keeps its own descriptor once the root is closed.
	defer root.Close()
	return root.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}

func readAttachmentFile(index int, in draftAttachmentInput, roots attachmentRoots) (bridgeclient.DraftAttachment, error) {
	// Fail closed on Windows: resolving a junction that targets \\host\share
	// authenticates to the remote SMB host before any containment check.
	if runtime.GOOS == "windows" {
		return bridgeclient.DraftAttachment{}, fmt.Errorf("attachment %d content_path is not supported on Windows; use content_base64", index)
	}
	path := *in.ContentPath
	if !filepath.IsAbs(path) {
		return bridgeclient.DraftAttachment{}, fmt.Errorf("attachment %d content_path %q is not an absolute path", index, path)
	}
	// Refuse UNC and \\?\-style paths before any filesystem call: on Windows
	// merely resolving them can authenticate to a remote SMB host.
	if len(path) >= 2 && os.IsPathSeparator(path[0]) && os.IsPathSeparator(path[1]) {
		return bridgeclient.DraftAttachment{}, fmt.Errorf("attachment %d content_path %q is a UNC or device path, which is not supported", index, path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return bridgeclient.DraftAttachment{}, fmt.Errorf("attachment %d content_path %q: %w", index, path, err)
	}
	// Non-blocking pre-check so FIFOs and devices are refused before any open.
	info, err := os.Stat(resolved)
	if err != nil {
		return bridgeclient.DraftAttachment{}, fmt.Errorf("attachment %d content_path %q: %w", index, path, err)
	}
	if !info.Mode().IsRegular() {
		return bridgeclient.DraftAttachment{}, fmt.Errorf("attachment %d content_path %q is not a regular file", index, path)
	}
	f, err := openAttachmentFile(resolved, roots)
	if err != nil {
		return bridgeclient.DraftAttachment{}, fmt.Errorf("attachment %d content_path %q: %w", index, path, err)
	}
	defer f.Close()
	// fstat binds the checks to the object actually opened, not the pathname.
	info, err = f.Stat()
	if err != nil {
		return bridgeclient.DraftAttachment{}, fmt.Errorf("attachment %d content_path %q: %w", index, path, err)
	}
	if !info.Mode().IsRegular() {
		return bridgeclient.DraftAttachment{}, fmt.Errorf("attachment %d content_path %q is not a regular file", index, path)
	}
	if info.Size() > bridgeclient.MaxDraftAttachmentBytes {
		return bridgeclient.DraftAttachment{}, fmt.Errorf("attachment %d content_path %q is %d bytes, above the %d byte limit", index, path, info.Size(), bridgeclient.MaxDraftAttachmentBytes)
	}
	data, err := io.ReadAll(io.LimitReader(f, bridgeclient.MaxDraftAttachmentBytes+1))
	if err != nil {
		return bridgeclient.DraftAttachment{}, fmt.Errorf("attachment %d content_path %q: %w", index, path, err)
	}
	// The file may have grown between Stat and the read.
	if len(data) > bridgeclient.MaxDraftAttachmentBytes {
		return bridgeclient.DraftAttachment{}, fmt.Errorf("attachment %d content_path %q is %d bytes or more, above the %d byte limit", index, path, len(data), bridgeclient.MaxDraftAttachmentBytes)
	}
	filename := in.Filename
	if filename == "" {
		filename = filepath.Base(path)
	}
	contentType := in.ContentType
	if contentType == "" {
		if contentType = mime.TypeByExtension(filepath.Ext(filename)); contentType == "" {
			contentType = "application/octet-stream"
		}
	}
	return bridgeclient.DraftAttachment{Filename: filename, ContentType: contentType, Data: data}, nil
}
