package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"regexp/syntax"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bmatcuk/doublestar/v4"
)

const (
	defaultMaxSearchMatches      = 100
	defaultSearchTruncateLineLen = 200
	maxSearchQueryLen            = 1024
	maxSearchMatchesPerRequest   = 1000
	defaultSearchMaxBytesPerFile = 1024 * 1024
	maxSearchRegexRepeats        = 12
	defaultSearchRegexTimeout    = 2 * time.Second
	searchMaxMatchesPerLine      = 5
)

var errSearchLimitReached = errors.New("search match limit reached")
var errSearchRegexTimeout = errors.New("regex search timed out")

type FileManager struct {
	rootAbs        string
	rootReal       string
	excludeDot     bool
	excludePattern *regexp.Regexp
}

type FileEntry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Type    string    `json:"type"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
}

type ReadFileResult struct {
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	ModTimeUTC string `json:"modTimeUtc"`
	Content    string `json:"content"`
	IsText     bool   `json:"isText"`
	Truncated  bool   `json:"truncated"`
}

type SearchMatch struct {
	Path      string `json:"path"`
	Line      int    `json:"line"`
	Column    int    `json:"column"`
	LineText  string `json:"line_text"`
	MatchText string `json:"match_text"`
}

type SearchOptions struct {
	Query            string
	CaseSensitive    bool
	CaseSensitiveSet bool
	Regex            bool
	FileGlob         string
	MaxMatches       int
	MaxBytesPerFile  int64
	RegexTimeout     time.Duration
}

func NewFileManager(root string, excludeDot bool, excludeRegex string) (*FileManager, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("root is required")
	}
	absRoot, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return nil, fmt.Errorf("resolve root: %w", err)
	}
	info, err := os.Stat(absRoot)
	if err != nil {
		return nil, fmt.Errorf("stat root: %w", err)
	}
	if !info.IsDir() {
		return nil, errors.New("root must be a directory")
	}
	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve root symlinks: %w", err)
	}
	realRoot, err = filepath.Abs(realRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve real root: %w", err)
	}

	var excludePattern *regexp.Regexp
	if strings.TrimSpace(excludeRegex) != "" {
		excludePattern, err = regexp.Compile(excludeRegex)
		if err != nil {
			return nil, fmt.Errorf("compile exclude-regex: %w", err)
		}
	}

	return &FileManager{
		rootAbs:        absRoot,
		rootReal:       realRoot,
		excludeDot:     excludeDot,
		excludePattern: excludePattern,
	}, nil
}

func (m *FileManager) List(path string, recursive bool) ([]FileEntry, error) {
	baseAbs, relToRoot, err := m.resolveAndValidate(path)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(baseAbs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("path is not a directory: %s", path)
	}

	if relToRoot != "." && m.shouldExclude(relToRoot) {
		return nil, fmt.Errorf("path excluded by policy: %s", path)
	}

	var entries []FileEntry
	if recursive {
		err = filepath.WalkDir(baseAbs, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if os.IsPermission(err) {
					if d != nil && d.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
				return err
			}
			if path == baseAbs {
				return nil
			}
			rel, err := filepath.Rel(m.rootReal, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(filepath.Clean(rel))

			if _, err := m.validateResolvedPath(path, rel); err != nil {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if m.shouldExclude(rel) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}

			info, err := d.Info()
			if err != nil {
				return err
			}
			entryType := "file"
			if d.IsDir() {
				entryType = "directory"
			}
			entries = append(entries, FileEntry{
				Name:    d.Name(),
				Path:    rel,
				Type:    entryType,
				Size:    info.Size(),
				ModTime: info.ModTime().UTC(),
			})
			return nil
		})
	} else {
		dirEntries, err := os.ReadDir(baseAbs)
		if err != nil {
			return nil, err
		}
		for _, entry := range dirEntries {
			rel := entry.Name()
			if relToRoot != "." {
				rel = filepath.ToSlash(filepath.Join(relToRoot, entry.Name()))
			}
			entryAbs := filepath.Join(baseAbs, entry.Name())
			if _, err := m.validateResolvedPath(entryAbs, rel); err != nil {
				continue
			}
			if m.shouldExclude(rel) {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				return nil, err
			}
			entryType := "file"
			if entry.IsDir() {
				entryType = "directory"
			}
			entries = append(entries, FileEntry{
				Name:    entry.Name(),
				Path:    rel,
				Type:    entryType,
				Size:    info.Size(),
				ModTime: info.ModTime().UTC(),
			})
		}
	}
	if err != nil {
		return nil, err
	}

	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].Path) < strings.ToLower(entries[j].Path)
	})
	return entries, nil
}

func (m *FileManager) ReadFile(path string, maxBytes int64) (*ReadFileResult, error) {
	if maxBytes <= 0 {
		return nil, errors.New("maxBytes must be > 0")
	}
	abs, rel, err := m.resolveAndValidate(path)
	if err != nil {
		return nil, err
	}
	if m.shouldExclude(rel) {
		return nil, fmt.Errorf("path excluded by policy: %s", path)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, errors.New("cannot read directory")
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	limit := maxBytes
	if info.Size() < limit {
		limit = info.Size()
	}
	raw, err := io.ReadAll(io.LimitReader(f, limit))
	if err != nil {
		return nil, err
	}

	return &ReadFileResult{
		Path:       rel,
		Size:       info.Size(),
		ModTimeUTC: info.ModTime().UTC().Format(time.RFC3339),
		Content:    string(raw),
		IsText:     utf8.Valid(raw),
		Truncated:  info.Size() > int64(limit),
	}, nil
}

func (m *FileManager) SearchFiles(path string, opts SearchOptions) ([]SearchMatch, bool, error) {
	query := strings.TrimSpace(opts.Query)
	if query == "" {
		return nil, false, errors.New("query is required")
	}
	if len(query) > maxSearchQueryLen {
		return nil, false, errors.New("query too long")
	}

	baseAbs, relToRoot, err := m.resolveAndValidate(path)
	if err != nil {
		return nil, false, err
	}
	if m.shouldExclude(relToRoot) {
		return nil, false, fmt.Errorf("path excluded by policy: %s", path)
	}

	maxMatches := opts.MaxMatches
	if maxMatches <= 0 {
		maxMatches = defaultMaxSearchMatches
	}
	if maxMatches > maxSearchMatchesPerRequest {
		maxMatches = maxSearchMatchesPerRequest
	}

	maxBytesPerFile := opts.MaxBytesPerFile
	if maxBytesPerFile <= 0 {
		maxBytesPerFile = defaultSearchMaxBytesPerFile
	}

	info, err := os.Stat(baseAbs)
	if err != nil {
		return nil, false, err
	}
	if !info.IsDir() {
		return nil, false, fmt.Errorf("path is not a directory: %s", path)
	}

	fileGlob := strings.TrimSpace(opts.FileGlob)
	if fileGlob != "" {
		if err := validateFileGlobPattern(fileGlob); err != nil {
			return nil, false, fmt.Errorf("invalid file_glob: %w", err)
		}
	}

	caseSensitive := opts.CaseSensitive
	if !opts.CaseSensitiveSet {
		caseSensitive = true
	}

	searcher, err := m.makeSearchMatcher(query, caseSensitive, opts.Regex)
	if err != nil {
		return nil, false, err
	}

	regexDeadline := time.Time{}
	if opts.Regex {
		timeout := opts.RegexTimeout
		if timeout <= 0 {
			timeout = defaultSearchRegexTimeout
		}
		if timeout > 0 {
			regexDeadline = time.Now().Add(timeout)
		}
	}

	var matches []SearchMatch
	limitReached := false

	err = filepath.WalkDir(baseAbs, func(current string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsPermission(walkErr) {
				if d != nil && d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			return walkErr
		}

		rel, relErr := filepath.Rel(m.rootReal, current)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(filepath.Clean(rel))

		if rel == "." {
			return nil
		}
		if _, err := m.validateResolvedPath(current, rel); err != nil {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if m.shouldExclude(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if fileGlob != "" {
			matchesPattern, matchErr := doublestar.Match(filepath.ToSlash(fileGlob), rel)
			if matchErr != nil {
				return matchErr
			}
			if !matchesPattern {
				return nil
			}
		}

		fileInfo, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		if !fileInfo.Mode().IsRegular() {
			return nil
		}
		if fileInfo.Size() > maxBytesPerFile {
			return nil
		}

		remaining := maxMatches - len(matches)
		if remaining <= 0 {
			limitReached = true
			return errSearchLimitReached
		}

		fileMatches, findErr := m.searchInFile(current, rel, searcher, remaining, regexDeadline, opts.Regex)
		if findErr != nil {
			if errors.Is(findErr, errSearchRegexTimeout) {
				return findErr
			}
			return findErr
		}
		for _, match := range fileMatches {
			matches = append(matches, match)
			if len(matches) >= maxMatches {
				limitReached = true
				return errSearchLimitReached
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, errSearchLimitReached) {
		if errors.Is(err, errSearchRegexTimeout) {
			return nil, false, err
		}
		return nil, false, err
	}
	return matches, limitReached, nil
}

func (m *FileManager) makeSearchMatcher(query string, caseSensitive, useRegex bool) (func(string) []int, error) {
	if useRegex {
		pattern := query
		if !caseSensitive {
			pattern = "(?i)" + pattern
		}
		re, err := compileSafeRegex(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid regex pattern: %w", err)
		}
		return func(line string) []int {
			locs := re.FindAllStringIndex(line, searchMaxMatchesPerLine)
			if len(locs) == 0 {
				return nil
			}
			// Flatten indexes: [start,end,start,end,...]
			var result []int
			for _, loc := range locs {
				if len(loc) == 2 {
					result = append(result, loc[0], loc[1])
				}
			}
			return result
		}, nil
	}

	var queryText string
	if caseSensitive {
		queryText = query
	} else {
		queryText = strings.ToLower(query)
	}

	return func(line string) []int {
		needle := line
		if !caseSensitive {
			needle = strings.ToLower(line)
		}
		if queryText == "" {
			return nil
		}
		var locs []int
		offset := 0
		for {
			idx := strings.Index(needle[offset:], queryText)
			if idx < 0 {
				break
			}
			start := offset + idx
			end := start + len(queryText)
			locs = append(locs, start, end)
			offset = end
			if len(locs)/2 >= searchMaxMatchesPerLine {
				break
			}
			if offset >= len(needle) {
				break
			}
		}
		return locs
	}, nil
}

func (m *FileManager) searchInFile(absPath string, relPath string, matcher func(string) []int, remaining int, regexDeadline time.Time, regexMode bool) ([]SearchMatch, error) {
	f, err := os.Open(absPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sample := make([]byte, 4096)
	n, readErr := f.Read(sample)
	if readErr != nil && readErr != io.EOF {
		return nil, readErr
	}
	if !utf8.Valid(sample[:n]) {
		return nil, nil
	}
	if _, seekErr := f.Seek(0, 0); seekErr != nil {
		return nil, seekErr
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	lineNo := 1
	var matches []SearchMatch

	for scanner.Scan() {
		if regexMode && !regexDeadline.IsZero() && time.Now().After(regexDeadline) {
			return nil, errSearchRegexTimeout
		}
		line := scanner.Text()
		locs := matcher(line)
		for i := 0; i+1 < len(locs); i += 2 {
			start, end := locs[i], locs[i+1]
			if start < 0 || end < start {
				continue
			}
			matchText := line[start:end]
			matches = append(matches, SearchMatch{
				Path:      relPath,
				Line:      lineNo,
				Column:    start + 1,
				LineText:  truncateLineForMatch(line, start, end),
				MatchText: matchText,
			})
			if len(matches) >= remaining {
				return matches, nil
			}
		}
		lineNo++
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return nil, scanErr
	}
	return matches, nil
}

func truncateLineForMatch(line string, start, end int) string {
	if start < 0 || end < start || start > len(line) || end > len(line) {
		return line
	}
	if len(line) <= defaultSearchTruncateLineLen {
		return line
	}
	if end-start >= defaultSearchTruncateLineLen {
		snippet := line[start : start+defaultSearchTruncateLineLen]
		if end > start+defaultSearchTruncateLineLen {
			return snippet + "…"
		}
		return snippet
	}

	maxLen := defaultSearchTruncateLineLen
	matchLen := end - start
	remaining := maxLen - matchLen
	leftBudget := remaining / 2
	rightBudget := remaining - leftBudget

	leftAvail := start
	rightAvail := len(line) - end
	leftTake := leftAvail
	rightTake := rightAvail
	if leftTake > leftBudget {
		leftTake = leftBudget
	}
	if rightTake > rightBudget {
		rightTake = rightBudget
	}
	if leftTake < leftBudget {
		rightTake += leftBudget - leftTake
		if rightTake > rightAvail {
			rightTake = rightAvail
		}
	}
	if rightTake < rightBudget {
		leftTake += rightBudget - rightTake
		if leftTake > leftAvail {
			leftTake = leftAvail
		}
	}
	if rightTake > rightAvail {
		rightTake = rightAvail
	}
	if leftTake > leftAvail {
		leftTake = leftAvail
	}

	cutStart := start - leftTake
	cutEnd := end + rightTake
	if cutStart < 0 {
		cutStart = 0
	}
	if cutEnd > len(line) {
		cutEnd = len(line)
	}
	ellipsisCount := 0
	if cutStart > 0 {
		ellipsisCount++
	}
	if cutEnd < len(line) {
		ellipsisCount++
	}
	maxContentLen := defaultSearchTruncateLineLen + 2 - ellipsisCount*len("…")
	if maxContentLen < matchLen {
		maxContentLen = matchLen
	}

	if cutEnd-cutStart > maxContentLen {
		excess := (cutEnd - cutStart) - maxContentLen
		trimRight := minInt(excess/2, cutEnd-end)
		cutEnd -= trimRight
		excess -= trimRight
		trimLeft := minInt(excess, start-cutStart)
		cutStart += trimLeft
		excess -= trimLeft
		if excess > 0 {
			trimRight = minInt(excess, cutEnd-end)
			cutEnd -= trimRight
			excess -= trimRight
		}
		if excess > 0 {
			cutStart += minInt(excess, start-cutStart)
		}
	}

	prefixText := line[0:cutStart]
	suffixText := line[cutEnd:]
	middle := line[cutStart:cutEnd]

	middleLine := middle
	if prefixText != "" {
		middleLine = "…" + middleLine
	}
	if suffixText != "" {
		middleLine = middleLine + "…"
	}
	return middleLine
}

func validateFileGlobPattern(pattern string) error {
	if strings.TrimSpace(pattern) == "" {
		return errors.New("glob pattern is empty")
	}
	if filepath.IsAbs(pattern) {
		return errors.New("absolute patterns are not allowed")
	}

	normalized := filepath.FromSlash(pattern)
	clean := filepath.Clean(normalized)
	if filepath.IsAbs(clean) {
		return errors.New("absolute patterns are not allowed")
	}
	for _, segment := range strings.Split(clean, string(filepath.Separator)) {
		if segment == ".." {
			return errors.New("glob pattern contains forbidden '..' segment")
		}
	}
	patternToMatch := filepath.ToSlash(pattern)
	_, err := doublestar.Match(patternToMatch, "sample.txt")
	if err != nil {
		return err
	}
	return nil
}

func compileSafeRegex(pattern string) (*regexp.Regexp, error) {
	parsed, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return nil, err
	}
	if err := inspectRegexComplexity(parsed); err != nil {
		return nil, err
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	return re, nil
}

func inspectRegexComplexity(re *syntax.Regexp) error {
	repeatCount := 0
	if err := hasNestedOrTooManyRepeats(re, false, &repeatCount); err != nil {
		return err
	}
	if repeatCount > maxSearchRegexRepeats {
		return fmt.Errorf("pattern contains too many repetition operators")
	}
	return nil
}

func hasNestedOrTooManyRepeats(re *syntax.Regexp, inRepeat bool, repeatCount *int) error {
	if re == nil {
		return nil
	}
	op := re.Op
	isRepeat := op == syntax.OpStar || op == syntax.OpPlus || op == syntax.OpQuest || op == syntax.OpRepeat
	if isRepeat {
		*repeatCount++
		if inRepeat {
			return fmt.Errorf("pattern has nested repetition operators")
		}
		for _, child := range re.Sub {
			if err := hasNestedOrTooManyRepeats(child, true, repeatCount); err != nil {
				return err
			}
		}
		return nil
	}
	for _, child := range re.Sub {
		if err := hasNestedOrTooManyRepeats(child, inRepeat, repeatCount); err != nil {
			return err
		}
	}
	return nil
}

func (m *FileManager) resolveAndValidate(path string) (abs string, rel string, err error) {
	clean := filepath.Clean(filepath.FromSlash(path))
	if clean == "." {
		clean = "."
	}
	if filepath.IsAbs(clean) {
		return "", "", errors.New("absolute paths are not allowed")
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", "", errors.New("path traversal blocked")
	}

	joined := filepath.Join(m.rootAbs, clean)
	candidate, err := filepath.Abs(joined)
	if err != nil {
		return "", "", err
	}

	relToRoot, err := filepath.Rel(m.rootAbs, candidate)
	if err != nil {
		return "", "", err
	}
	if relToRoot == "." {
		relToRoot = "."
	}
	if relToRoot == ".." || strings.HasPrefix(relToRoot, ".."+string(filepath.Separator)) || strings.HasPrefix(relToRoot, string(filepath.Separator)) {
		return "", "", errors.New("path traversal blocked")
	}
	relToRoot = filepath.ToSlash(relToRoot)
	resolved, err := m.validateResolvedPath(candidate, relToRoot)
	if err != nil {
		return "", "", err
	}
	return resolved, relToRoot, nil
}

func (m *FileManager) validateResolvedPath(candidate string, rel string) (string, error) {
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", err
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", err
	}

	relToRoot, err := filepath.Rel(m.rootReal, resolved)
	if err != nil {
		return "", err
	}
	if relToRoot == ".." || strings.HasPrefix(relToRoot, ".."+string(filepath.Separator)) || strings.HasPrefix(relToRoot, string(filepath.Separator)) {
		return "", errors.New("path escapes root via symlink")
	}

	expected := m.rootReal
	if rel != "." {
		expected = filepath.Join(m.rootReal, filepath.FromSlash(rel))
	}
	if resolved != expected {
		return "", errors.New("symlinks are not allowed")
	}
	return resolved, nil
}

func (m *FileManager) shouldExclude(relPath string) bool {
	normalized := filepath.ToSlash(relPath)
	if normalized == "." || normalized == "" {
		return false
	}
	if m.excludeDot && hasDotSegment(normalized) {
		return true
	}
	if m.excludePattern != nil && m.excludePattern.MatchString(normalized) {
		return true
	}
	return false
}

func hasDotSegment(path string) bool {
	parts := strings.Split(path, "/")
	for _, part := range parts {
		if part != "" && strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
