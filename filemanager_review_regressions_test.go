package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestFileManagerListSkipsUnreadableDirectoryDuringRecursiveWalk(t *testing.T) {
	blockedDir := createUnreadableDirOrSkip(t)

	root := filepath.Dir(blockedDir)
	readablePath := filepath.Join(root, "readable.txt")
	if err := os.WriteFile(readablePath, []byte("still visible"), 0o644); err != nil {
		t.Fatalf("write readable file: %v", err)
	}

	manager, err := NewFileManager(root, true, "")
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	entries, err := manager.List(".", true, "")
	if err != nil {
		t.Fatalf("expected recursive list to skip unreadable entries, got error: %v", err)
	}

	foundReadable := false
	for _, entry := range entries {
		if entry.Path == "readable.txt" {
			foundReadable = true
			break
		}
	}
	if !foundReadable {
		t.Fatalf("expected recursive list to keep readable siblings")
	}
}

func TestFileManagerSearchFilesSkipsUnreadableDirectoryDuringWalk(t *testing.T) {
	blockedDir := createUnreadableDirOrSkip(t)

	root := filepath.Dir(blockedDir)
	keepPath := filepath.Join(root, "keep.txt")
	if err := os.WriteFile(keepPath, []byte("needle in readable file"), 0o644); err != nil {
		t.Fatalf("write readable file: %v", err)
	}

	manager, err := NewFileManager(root, true, "")
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	matches, truncated, err := manager.SearchFiles(".", SearchOptions{
		Query:      "needle",
		MaxMatches: 10,
	})
	if err != nil {
		t.Fatalf("expected search to skip unreadable entries, got error: %v", err)
	}
	if truncated {
		t.Fatalf("expected no truncation")
	}
	if len(matches) != 1 {
		t.Fatalf("expected one readable match, got %d", len(matches))
	}
	if matches[0].Path != "keep.txt" {
		t.Fatalf("expected match in keep.txt, got %q", matches[0].Path)
	}
}

func TestFileManagerCaseInsensitiveSearchReportsCorrectUnicodeOffsets(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "unicode.txt"), []byte("İx\n"), 0o644); err != nil {
		t.Fatalf("write unicode file: %v", err)
	}

	manager, err := NewFileManager(root, true, "")
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	matches, truncated, err := manager.SearchFiles(".", SearchOptions{
		Query:            "X",
		CaseSensitive:    false,
		CaseSensitiveSet: true,
		MaxMatches:       10,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if truncated {
		t.Fatalf("expected no truncation")
	}
	if len(matches) != 1 {
		t.Fatalf("expected one match, got %d", len(matches))
	}
	if matches[0].Path != "unicode.txt" {
		t.Fatalf("expected unicode.txt match, got %q", matches[0].Path)
	}
	if matches[0].Column != 3 {
		t.Fatalf("expected byte column for x to be 3, got %d", matches[0].Column)
	}
	if matches[0].MatchText != "x" {
		t.Fatalf("expected match text x, got %q", matches[0].MatchText)
	}
}

func TestFileManagerSearchFilesHandlesLongSingleLineWithinAllowedLimit(t *testing.T) {
	root := t.TempDir()
	content := strings.Repeat("a", (1<<20)+128) + "needle"
	if err := os.WriteFile(filepath.Join(root, "long-line.txt"), []byte(content), 0o644); err != nil {
		t.Fatalf("write long-line file: %v", err)
	}

	manager, err := NewFileManager(root, true, "")
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	matches, truncated, err := manager.SearchFiles(".", SearchOptions{
		Query:           "needle",
		MaxMatches:      10,
		MaxBytesPerFile: int64(len(content) + 1),
	})
	if err != nil {
		t.Fatalf("expected long line within allowed limit to be searchable, got error: %v", err)
	}
	if truncated {
		t.Fatalf("expected no truncation")
	}
	if len(matches) != 1 {
		t.Fatalf("expected one match in long-line file, got %d", len(matches))
	}
	if matches[0].Path != "long-line.txt" {
		t.Fatalf("expected match in long-line.txt, got %q", matches[0].Path)
	}
}

func TestFileManagerSearchFilesPseudoFile(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("skipping pseudo-file test on non-linux")
	}

	// Double check /proc/version exists and has size 0
	info, err := os.Stat("/proc/version")
	if err != nil {
		t.Skipf("skipping: %v", err)
	}
	if info.Size() != 0 {
		t.Skip("skipping: /proc/version size is not 0 on this system")
	}

	manager, err := NewFileManager("/proc", true, "")
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	matches, truncated, err := manager.SearchFiles("version", SearchOptions{
		Query:           "Linux",
		MaxMatches:      10,
		MaxBytesPerFile: 1024,
	})
	
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if truncated {
		t.Fatalf("expected no truncation")
	}
	if len(matches) == 0 {
		t.Fatalf("expected match in /proc/version (which reports size 0), got 0 matches")
	}
}

func createUnreadableDirOrSkip(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("permission masking differs on windows")
	}

	root := t.TempDir()
	blockedDir := filepath.Join(root, "blocked")
	if err := os.Mkdir(blockedDir, 0o755); err != nil {
		t.Fatalf("mkdir blocked dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(blockedDir, "secret.txt"), []byte("needle in blocked file"), 0o644); err != nil {
		t.Fatalf("write blocked file: %v", err)
	}
	if err := os.Chmod(blockedDir, 0); err != nil {
		t.Skipf("failed to make directory unreadable: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(blockedDir, 0o755)
	})
	return blockedDir
}
