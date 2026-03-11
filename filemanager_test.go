package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileManagerListFilters(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "readme.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "skip.txt"), []byte("skip"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, ".hidden"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".hidden", "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatalf("write hidden file: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "include.txt"), []byte("nested"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".nested", "exclude.txt"), []byte("nope"), 0o644); err == nil {
		t.Fatalf("expected .nested dir not to exist")
	}

	manager, err := NewFileManager(root, true, `skip\.txt$`)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	entries, err := manager.List(".", true, "")
	if err != nil {
		t.Fatalf("list recursive: %v", err)
	}

	pathSet := map[string]struct{}{}
	for _, e := range entries {
		pathSet[e.Path] = struct{}{}
	}

	if _, ok := pathSet["."]; ok {
		t.Fatalf("root should not appear in recursive listing")
	}
	if _, ok := pathSet["readme.txt"]; !ok {
		t.Fatalf("expected readme.txt in result")
	}
	if _, ok := pathSet["skip.txt"]; ok {
		t.Fatalf("expected skip.txt to be excluded by regex")
	}
	if _, ok := pathSet[".hidden"]; ok {
		t.Fatalf("expected hidden dir to be excluded")
	}
	if _, ok := pathSet[".hidden/secret.txt"]; ok {
		t.Fatalf("expected hidden secret file to be excluded")
	}
	if _, ok := pathSet["nested/include.txt"]; !ok {
		t.Fatalf("expected nested/include.txt in result")
	}
}

func TestFileManagerListExcludesByCombinedRegex(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "keep.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "drop.txt"), []byte("drop"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	manager, err := NewFileManager(root, true, `^(?:keep|drop)\.txt$`)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	entries, err := manager.List(".", false, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected all files filtered out, got %d entries", len(entries))
	}
}

func TestFileManagerTraversalBlocked(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "safe.txt"), []byte("safe"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	manager, err := NewFileManager(root, true, "")
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if _, err := manager.ReadFile("../safe.txt", 64, 0, 0); err == nil {
		t.Fatalf("expected traversal path to be rejected")
	}
	if _, err := manager.List("../", false, ""); err == nil {
		t.Fatalf("expected traversal directory to be rejected")
	}
}

func TestFileManagerReadFileRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir()
	target := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(target, []byte("secret"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	mustSymlinkOrSkip(t, target, filepath.Join(root, "secret-link.txt"))

	manager, err := NewFileManager(root, true, "")
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	if _, err := manager.ReadFile("secret-link.txt", 64, 0, 0); err == nil {
		t.Fatalf("expected symlink escape to be rejected")
	}
}

func TestFileManagerListRejectsSymlinkDirectoryEscape(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideDir, "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	mustSymlinkOrSkip(t, outsideDir, filepath.Join(root, "outside"))

	manager, err := NewFileManager(root, true, "")
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	if _, err := manager.List("outside", false, ""); err == nil {
		t.Fatalf("expected symlinked directory outside root to be rejected")
	}
}

func TestFileManagerSearchFilesSkipsSymlinkedFilesOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir()
	target := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(target, []byte("needle outside root"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	mustSymlinkOrSkip(t, target, filepath.Join(root, "secret-link.txt"))

	manager, err := NewFileManager(root, true, "")
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	matches, truncated, err := manager.SearchFiles(".", SearchOptions{
		Query:      "needle",
		MaxMatches: 10,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if truncated {
		t.Fatalf("expected no truncation")
	}
	if len(matches) != 0 {
		t.Fatalf("expected outside-root symlink target to be skipped, got %d matches", len(matches))
	}
}

func TestFileManagerReadFileTruncates(t *testing.T) {
	root := t.TempDir()
	content := "abcdefghijklmnopqrstuvwxyz"
	if err := os.WriteFile(filepath.Join(root, "long.txt"), []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	manager, err := NewFileManager(root, true, "")
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	result, err := manager.ReadFile("long.txt", 10, 0, 0)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if !result.Truncated {
		t.Fatalf("expected truncation flag")
	}
	if len(result.Content) != 10 {
		t.Fatalf("expected 10 bytes, got %d", len(result.Content))
	}
	if !result.IsText {
		t.Fatalf("expected text file to be detected as text")
	}
}

func TestFileManagerSearchFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "readme.md"), []byte("alpha beta\nline two\nAlpha again"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("no match here"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "image.bin"), []byte{0x00, 0x12, 0x03}, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	manager, err := NewFileManager(root, true, "")
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	matches, truncated, err := manager.SearchFiles(".", SearchOptions{
		Query:      "alpha",
		MaxMatches: 10,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if truncated {
		t.Fatalf("expected no truncation")
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if matches[0].Path != "readme.md" {
		t.Fatalf("expected match in readme.md, got %q", matches[0].Path)
	}
}

func TestFileManagerSearchFilesDoublestar(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src", "nested"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "nested", "main.go"), []byte("package main\nfunc main() {}\n// find_me"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "other.txt"), []byte("find_me too"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	manager, err := NewFileManager(root, true, "")
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	t.Run("match all go files", func(t *testing.T) {
		matches, _, err := manager.SearchFiles(".", SearchOptions{
			Query:    "find_me",
			FileGlob: "**/*.go",
		})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		if len(matches) != 1 {
			t.Fatalf("expected 1 match, got %d", len(matches))
		}
		if matches[0].Path != filepath.ToSlash(filepath.Join("src", "nested", "main.go")) {
			t.Fatalf("expected match in src/nested/main.go, got %q", matches[0].Path)
		}
	})

	t.Run("match text files", func(t *testing.T) {
		matches, _, err := manager.SearchFiles(".", SearchOptions{
			Query:    "find_me",
			FileGlob: "**/*.txt",
		})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		if len(matches) != 1 {
			t.Fatalf("expected 1 match, got %d", len(matches))
		}
		if matches[0].Path != filepath.ToSlash(filepath.Join("src", "other.txt")) {
			t.Fatalf("expected match in src/other.txt, got %q", matches[0].Path)
		}
	})
}

func TestFileManagerSearchFilesRejectsInvalidQuery(t *testing.T) {
	root := t.TempDir()
	manager, err := NewFileManager(root, true, "")
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	longQuery := make([]byte, maxSearchQueryLen+1)
	for i := 0; i < len(longQuery); i++ {
		longQuery[i] = 'a'
	}
	_, _, err = manager.SearchFiles(".", SearchOptions{
		Query: string(longQuery),
	})
	if err == nil {
		t.Fatalf("expected search to reject long query")
	}
}

func TestFileManagerSearchFilesRejectsInvalidGlob(t *testing.T) {
	root := t.TempDir()
	manager, err := NewFileManager(root, true, "")
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if _, _, err := manager.SearchFiles(".", SearchOptions{
		Query:    "alpha",
		FileGlob: "[",
	}); err == nil {
		t.Fatalf("expected search to reject invalid glob pattern")
	}
	if _, _, err := manager.SearchFiles(".", SearchOptions{
		Query:    "alpha",
		FileGlob: "../*.go",
	}); err == nil {
		t.Fatalf("expected search to reject glob with path traversal segment")
	}
}

func TestFileManagerSearchFilesRejectsUnsafeRegex(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "readme.md"), []byte("aaaaa"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	manager, err := NewFileManager(root, true, "")
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if _, _, err := manager.SearchFiles(".", SearchOptions{
		Query: "([a]+)+",
		Regex: true,
	}); err == nil {
		t.Fatalf("expected nested repetition regex to be rejected")
	}
}

func TestCompileSafeRegex(t *testing.T) {
	if _, err := compileSafeRegex("([a]+)+"); err == nil {
		t.Fatalf("expected nested repetition regex to be rejected")
	}
	if _, err := compileSafeRegex("alpha"); err != nil {
		t.Fatalf("expected literal regex to be accepted, got %v", err)
	}
}

func TestTruncateLineForMatch(t *testing.T) {
	line := strings.Repeat("a", 250) + "match_here" + strings.Repeat("b", 250)
	start := strings.Index(line, "match_here")
	if start < 0 {
		t.Fatalf("expected match text in line")
	}
	end := start + len("match_here")

	snippet := truncateLineForMatch(line, start, end)
	if snippet == line {
		t.Fatalf("expected truncated snippet")
	}
	if !strings.Contains(snippet, "…") {
		t.Fatalf("expected truncated snippet to include ellipsis")
	}
	if !strings.Contains(snippet, "match_here") {
		t.Fatalf("expected snippet to contain match")
	}
	if len(snippet) > defaultSearchTruncateLineLen+2 {
		t.Fatalf("expected snippet within max length, got %d", len(snippet))
	}
}

func mustSymlinkOrSkip(t *testing.T, target string, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported in test environment: %v", err)
	}
}

// --- list_files glob tests ---

func TestFileManagerListGlobMatchesPattern(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "src", "pkg"), 0o755)
	os.WriteFile(filepath.Join(root, "readme.md"), []byte("hi"), 0o644)
	os.WriteFile(filepath.Join(root, "main.go"), []byte("package main"), 0o644)
	os.WriteFile(filepath.Join(root, "src", "lib.go"), []byte("package src"), 0o644)
	os.WriteFile(filepath.Join(root, "src", "lib_test.go"), []byte("package src"), 0o644)
	os.WriteFile(filepath.Join(root, "src", "pkg", "deep.go"), []byte("package pkg"), 0o644)
	os.WriteFile(filepath.Join(root, "src", "notes.txt"), []byte("note"), 0o644)

	manager, err := NewFileManager(root, false, "")
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	entries, err := manager.List(".", false, "**/*.go")
	if err != nil {
		t.Fatalf("list with glob: %v", err)
	}

	paths := map[string]bool{}
	for _, e := range entries {
		paths[e.Path] = true
	}

	for _, want := range []string{"main.go", "src/lib.go", "src/lib_test.go", "src/pkg/deep.go"} {
		if !paths[want] {
			t.Errorf("expected glob to match %q, got paths: %v", want, paths)
		}
	}
	for _, reject := range []string{"readme.md", "src/notes.txt"} {
		if paths[reject] {
			t.Errorf("expected glob to exclude %q", reject)
		}
	}
}

func TestFileManagerListGlobSingleStar(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "sub"), 0o755)
	os.WriteFile(filepath.Join(root, "top.go"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(root, "sub", "nested.go"), []byte("x"), 0o644)

	manager, err := NewFileManager(root, false, "")
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	entries, err := manager.List(".", false, "*.go")
	if err != nil {
		t.Fatalf("list with glob: %v", err)
	}

	paths := map[string]bool{}
	for _, e := range entries {
		paths[e.Path] = true
	}

	if !paths["top.go"] {
		t.Errorf("expected *.go to match top.go")
	}
	if paths["sub/nested.go"] {
		t.Errorf("expected *.go NOT to match sub/nested.go (single star should not cross directories)")
	}
}

func TestFileManagerListGlobRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	manager, err := NewFileManager(root, false, "")
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	_, err = manager.List(".", false, "../../etc/*")
	if err == nil {
		t.Fatalf("expected error for traversal glob pattern")
	}
}

func TestFileManagerListGlobRespectsExclusions(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "visible.go"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(root, "skip_me.go"), []byte("x"), 0o644)

	manager, err := NewFileManager(root, false, `skip_me`)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	entries, err := manager.List(".", false, "*.go")
	if err != nil {
		t.Fatalf("list with glob: %v", err)
	}

	for _, e := range entries {
		if e.Path == "skip_me.go" {
			t.Fatalf("expected skip_me.go to be excluded by regex policy")
		}
	}
	if len(entries) != 1 || entries[0].Path != "visible.go" {
		t.Fatalf("expected only visible.go, got %v", entries)
	}
}

func TestFileManagerListGlobEmptyStringIgnored(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("x"), 0o644)

	manager, err := NewFileManager(root, false, "")
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	entries, err := manager.List(".", false, "")
	if err != nil {
		t.Fatalf("list with empty glob: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry with empty glob (non-recursive), got %d", len(entries))
	}
}

// --- read_file offset/limit tests ---

func TestFileManagerReadFileLineRange(t *testing.T) {
	root := t.TempDir()
	lines := "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\n"
	os.WriteFile(filepath.Join(root, "ten.txt"), []byte(lines), 0o644)

	manager, err := NewFileManager(root, false, "")
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	result, err := manager.ReadFile("ten.txt", 1024, 3, 4)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}

	if result.StartLine != 3 {
		t.Errorf("expected StartLine=3, got %d", result.StartLine)
	}
	if result.EndLine != 6 {
		t.Errorf("expected EndLine=6, got %d", result.EndLine)
	}
	if result.TotalLines != 10 {
		t.Errorf("expected TotalLines=10, got %d", result.TotalLines)
	}
	if result.Content != "line3\nline4\nline5\nline6" {
		t.Errorf("unexpected content: %q", result.Content)
	}
	if !result.Truncated {
		t.Errorf("expected truncated=true (more lines exist beyond the range)")
	}
}

func TestFileManagerReadFileOffsetOnly(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "five.txt"), []byte("a\nb\nc\nd\ne\n"), 0o644)

	manager, err := NewFileManager(root, false, "")
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	result, err := manager.ReadFile("five.txt", 1024, 4, 0)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}

	if result.StartLine != 4 {
		t.Errorf("expected StartLine=4, got %d", result.StartLine)
	}
	if result.EndLine != 5 {
		t.Errorf("expected EndLine=5, got %d", result.EndLine)
	}
	if result.Content != "d\ne" {
		t.Errorf("unexpected content: %q", result.Content)
	}
	if result.Truncated {
		t.Errorf("expected truncated=false (reading to end of file)")
	}
}

func TestFileManagerReadFileLimitOnly(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "five.txt"), []byte("a\nb\nc\nd\ne\n"), 0o644)

	manager, err := NewFileManager(root, false, "")
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	result, err := manager.ReadFile("five.txt", 1024, 0, 2)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}

	if result.StartLine != 1 {
		t.Errorf("expected StartLine=1, got %d", result.StartLine)
	}
	if result.EndLine != 2 {
		t.Errorf("expected EndLine=2, got %d", result.EndLine)
	}
	if result.Content != "a\nb" {
		t.Errorf("unexpected content: %q", result.Content)
	}
	if !result.Truncated {
		t.Errorf("expected truncated=true")
	}
}

func TestFileManagerReadFileOffsetBeyondEnd(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "short.txt"), []byte("one\ntwo\n"), 0o644)

	manager, err := NewFileManager(root, false, "")
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	result, err := manager.ReadFile("short.txt", 1024, 100, 5)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}

	if result.Content != "" {
		t.Errorf("expected empty content for offset beyond EOF, got %q", result.Content)
	}
	if result.TotalLines != 2 {
		t.Errorf("expected TotalLines=2, got %d", result.TotalLines)
	}
	if result.EndLine != 0 {
		t.Errorf("expected EndLine=0 when no lines selected, got %d", result.EndLine)
	}
}

func TestFileManagerReadFileNoOffsetOrLimit(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "hello.txt"), []byte("hello world"), 0o644)

	manager, err := NewFileManager(root, false, "")
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	result, err := manager.ReadFile("hello.txt", 1024, 0, 0)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}

	if result.Content != "hello world" {
		t.Errorf("expected full content, got %q", result.Content)
	}
	if result.TotalLines != 0 {
		t.Errorf("expected TotalLines=0 in byte mode, got %d", result.TotalLines)
	}
	if result.StartLine != 0 {
		t.Errorf("expected StartLine=0 in byte mode, got %d", result.StartLine)
	}
}
