package mcptools

// The BARYON_ATTACHMENT_ROOTS boundary shared by save_draft's content_path
// reads and save_attachment's writes.

import (
	"os"
	"path/filepath"
)

// attachmentRoot is a configured root together with the identity of the
// directory it named when the server started.
type attachmentRoot struct {
	path string
	info os.FileInfo
}

// attachmentRoots is the confinement local file access runs under. configured
// records whether a boundary was asked for at all, which dirs alone cannot
// express: a configured boundary whose directories all became unreachable must
// refuse every path, not silently widen to the whole filesystem.
type attachmentRoots struct {
	configured bool
	dirs       []attachmentRoot
}

// pinAttachmentRoots records each root's identity once, at registration.
// Containment is then judged against that identity rather than against whatever
// the path resolves to at call time. Re-resolving instead would compare a
// replacement against itself and pass, so a root later renamed or replaced by a
// symlink would silently move the boundary with it.
func pinAttachmentRoots(roots []string) attachmentRoots {
	pinned := attachmentRoots{configured: len(roots) > 0}
	for _, root := range roots {
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			continue
		}
		pinned.dirs = append(pinned.dirs, attachmentRoot{path: root, info: info})
	}
	return pinned
}

// bind opens the root containing target and returns target's path relative to
// it. within reports containment and differs between reads (the target is the
// file) and writes (the target is its parent directory). The os.Root handle is
// checked against the pinned identity only after it is bound, leaving no window
// in which the name could be pointed elsewhere.
func (r attachmentRoots) bind(target string, within func(string, os.FileInfo) (string, bool)) (*os.Root, string, bool) {
	for _, root := range r.dirs {
		rel, ok := within(target, root.info)
		if !ok {
			continue
		}
		opened, err := os.OpenRoot(root.path)
		if err != nil {
			continue
		}
		current, err := opened.Stat(".")
		if err != nil || !os.SameFile(root.info, current) {
			_ = opened.Close()
			continue
		}
		return opened, rel, true
	}
	return nil, "", false
}

// relWithinRoot walks resolved's parents comparing directory identity with
// os.SameFile, so containment survives case-insensitive volume spellings that
// a lexical prefix check would miss.
func relWithinRoot(resolved string, rootInfo os.FileInfo) (string, bool) {
	rel := ""
	for cur := resolved; ; {
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", false
		}
		rel = filepath.Join(filepath.Base(cur), rel)
		info, err := os.Stat(parent)
		if err != nil {
			return "", false
		}
		if os.SameFile(rootInfo, info) {
			return rel, true
		}
		cur = parent
	}
}

// relDirWithinRoot is relWithinRoot for a directory: it also accepts the root
// itself, so a file written directly into an allowed root resolves to base.
func relDirWithinRoot(resolvedDir string, rootInfo os.FileInfo) (string, bool) {
	if info, err := os.Stat(resolvedDir); err == nil && os.SameFile(rootInfo, info) {
		return "", true
	}
	return relWithinRoot(resolvedDir, rootInfo)
}
