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
	entries, err := manager.List(".", true)
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
	entries, err := manager.List(".", false)
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
	if _, err := manager.ReadFile("../safe.txt", 64); err == nil {
		t.Fatalf("expected traversal path to be rejected")
	}
	if _, err := manager.List("../", false); err == nil {
		t.Fatalf("expected traversal directory to be rejected")
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
	result, err := manager.ReadFile("long.txt", 10)
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
