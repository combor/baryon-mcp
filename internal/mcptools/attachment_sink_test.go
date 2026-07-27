package mcptools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func resolveDir(t *testing.T, dir string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func TestWriteAttachmentFileWritesAndResolves(t *testing.T) {
	dir := resolveDir(t, t.TempDir())
	path := filepath.Join(dir, "report.pdf")

	saved, err := writeAttachmentFile(path, []byte("%PDF-data"), attachmentRoots{})
	if err != nil {
		t.Fatalf("writeAttachmentFile: %v", err)
	}
	if saved != path {
		t.Errorf("saved = %q, want %q", saved, path)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "%PDF-data" {
		t.Errorf("file = %q, %v", got, err)
	}
}

func TestWriteAttachmentFileRefusesOverwrite(t *testing.T) {
	dir := resolveDir(t, t.TempDir())
	path := writeTestFile(t, dir, "exists.bin", []byte("old"))

	if _, err := writeAttachmentFile(path, []byte("new"), attachmentRoots{}); err == nil {
		t.Fatal("expected refusal to overwrite an existing file")
	}
	if got, _ := os.ReadFile(path); string(got) != "old" {
		t.Errorf("existing file was modified: %q", got)
	}
}

func TestWriteAttachmentFileRejectsBadPaths(t *testing.T) {
	dir := resolveDir(t, t.TempDir())
	for _, tc := range []struct{ path, want string }{
		{"relative.pdf", "absolute path"},
		{"//server/share/file.pdf", "UNC or device path"},
		{filepath.Join(dir, "missing", "deep.pdf"), "parent directory"},
		{dir + string(filepath.Separator), "must name a file"},
	} {
		if _, err := writeAttachmentFile(tc.path, []byte("x"), attachmentRoots{}); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("path %q: error = %v, want mention of %q", tc.path, err, tc.want)
		}
	}
}

// The target name must never exist in a partial state, and the temporary file
// used to achieve that must not survive.
func TestWriteAttachmentFileLeavesNoTemporaryFile(t *testing.T) {
	dir := resolveDir(t, t.TempDir())
	path := filepath.Join(dir, "report.pdf")

	if _, err := writeAttachmentFile(path, []byte("%PDF-data"), attachmentRoots{}); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 1 || names[0] != "report.pdf" {
		t.Errorf("directory = %v, want only the finished file", names)
	}
}

// A refused write must not disturb the existing file, and must not leave its
// temporary file behind either.
func TestWriteAttachmentFileRefusedOverwriteLeavesDirectoryClean(t *testing.T) {
	dir := resolveDir(t, t.TempDir())
	path := writeTestFile(t, dir, "exists.bin", []byte("old"))

	if _, err := writeAttachmentFile(path, []byte("new"), attachmentRoots{}); err == nil {
		t.Fatal("expected refusal to overwrite an existing file")
	}
	if got, _ := os.ReadFile(path); string(got) != "old" {
		t.Errorf("existing file was modified: %q", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("directory = %d entries, want only the untouched original", len(entries))
	}
}

// Roots are pinned at startup. Replacing the root directory afterwards — the
// order a real swap would take — must not move the boundary: confinement is
// judged against the identity pinned then, not against the path now.
func TestWriteAttachmentFileRefusesRedirectedRoot(t *testing.T) {
	root := resolveDir(t, t.TempDir())
	outside := resolveDir(t, t.TempDir())

	pinned := pinAttachmentRoots([]string{root})
	if !pinned.configured || len(pinned.dirs) != 1 {
		t.Fatalf("pinned = %+v, want the configured root", pinned)
	}

	if err := os.Rename(root, root+".moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, root); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	_, err := writeAttachmentFile(filepath.Join(root, "planted.bin"), []byte("x"), pinned)
	if err == nil || !strings.Contains(err.Error(), "outside the directories") {
		t.Errorf("error = %v, want the redirected root refused", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "planted.bin")); err == nil {
		t.Error("write followed a root replaced by a symlink and escaped the configured directory")
	}
}

// A configured boundary whose directories are all gone must refuse every write.
// Treating "nothing left to allow" as "allow anything" would silently turn the
// restriction into unrestricted filesystem access.
func TestWriteAttachmentFileFailsClosedWhenNoRootPins(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "never-created")
	roots := pinAttachmentRoots([]string{missing})
	if !roots.configured || len(roots.dirs) != 0 {
		t.Fatalf("roots = %+v, want a configured boundary with nothing pinned", roots)
	}

	target := filepath.Join(resolveDir(t, t.TempDir()), "anywhere.bin")
	_, err := writeAttachmentFile(target, []byte("x"), roots)
	if err == nil || !strings.Contains(err.Error(), "outside the directories") {
		t.Errorf("error = %v, want the write refused", err)
	}
	if _, err := os.Stat(target); err == nil {
		t.Error("an unpinnable root allowed an unrestricted write")
	}
}

func TestWriteAttachmentFileEnforcesRoots(t *testing.T) {
	root := resolveDir(t, t.TempDir())
	outside := resolveDir(t, t.TempDir())

	saved, err := writeAttachmentFile(filepath.Join(root, "in.bin"), []byte("in"), pinAttachmentRoots([]string{root}))
	if err != nil || saved != filepath.Join(root, "in.bin") {
		t.Fatalf("inside root: (%q, %v), want success", saved, err)
	}

	if _, err := writeAttachmentFile(filepath.Join(outside, "out.bin"), []byte("out"), pinAttachmentRoots([]string{root})); err == nil || !strings.Contains(err.Error(), "outside the directories") {
		t.Errorf("outside root: error = %v, want root restriction", err)
	}

	// A symlinked parent that resolves outside the root must not redirect the write.
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := writeAttachmentFile(filepath.Join(link, "sneak.bin"), []byte("x"), pinAttachmentRoots([]string{root})); err == nil || !strings.Contains(err.Error(), "outside the directories") {
		t.Errorf("escape via symlink: error = %v, want root restriction", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "sneak.bin")); err == nil {
		t.Error("write escaped the root through a symlinked parent")
	}
}
