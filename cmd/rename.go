package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Marin-Kitagawa/Lyra/internal/tui"
	"github.com/Marin-Kitagawa/Lyra/internal/ui"
	"github.com/spf13/cobra"
)

var (
	renameDryRun  bool
	renameSeq     bool
	renameCase    string
	renameStart   int
	renameWidth   int
	renameRegex   bool
)

var renameCmd = &cobra.Command{
	Use:   "rename <pattern> <replacement>",
	Short: "Batch rename files with pattern matching",
	Long: `Batch rename files using glob patterns, regular expressions, or case conversions.

Glob mode (default):
  lyra rename "*.txt" "*.bak"
  lyra rename "report_*" "backup_*"

Regex mode (--regex):
  Capture groups are referenced with $1, $2, ${name} in the replacement.
  lyra rename --regex "^(.+)_v(\d+)\.txt$" "$1-$2.txt"
  lyra rename --regex "(?P<base>.+)\.jpeg$" "${base}.jpg"

Other modes:
  lyra rename --seq *.jpg               # Sequential: 001.jpg, 002.jpg, ...
  lyra rename --case upper *.txt        # UPPERCASE filenames
  lyra rename --case lower *.TXT        # lowercase filenames
  lyra rename --case title *.txt        # Title Case filenames

Add --dry-run to any mode to preview without making changes.`,
	Args: func(cmd *cobra.Command, args []string) error {
		if renameSeq || renameCase != "" {
			if len(args) < 1 {
				return fmt.Errorf("at least one file/pattern required")
			}
			return nil
		}
		if len(args) < 2 {
			return fmt.Errorf("requires <pattern> and <replacement> arguments")
		}
		return nil
	},
	RunE: runRename,
}

func init() {
	renameCmd.Flags().BoolVar(&renameDryRun, "dry-run", false, "Preview renames without making them")
	renameCmd.Flags().BoolVar(&renameSeq, "seq", false, "Sequential numbering rename")
	renameCmd.Flags().StringVar(&renameCase, "case", "", "Case conversion: upper, lower, title")
	renameCmd.Flags().IntVar(&renameStart, "start", 1, "Starting number for sequential rename")
	renameCmd.Flags().IntVar(&renameWidth, "width", 3, "Zero-padding width for sequential rename")
	renameCmd.Flags().BoolVarP(&renameRegex, "regex", "e", false, "Treat pattern as a regular expression; use $1/$2 or ${name} in replacement")
}

// renameOp represents a rename operation
type renameOp struct {
	oldPath string
	newPath string
}

func runRename(cmd *cobra.Command, args []string) error {
	var ops []renameOp
	var err error

	if renameSeq {
		ops, err = computeSeqRenames(args)
	} else if renameCase != "" {
		ops, err = computeCaseRenames(args, renameCase)
	} else if renameRegex {
		ops, err = computeRegexRenames(args[0], args[1])
	} else {
		ops, err = computePatternRenames(args[0], args[1])
	}

	if err != nil {
		return err
	}

	if len(ops) == 0 {
		fmt.Println(ui.RenderInfo("No files matched."))
		return nil
	}

	// Display preview
	for _, op := range ops {
		oldName := filepath.Base(op.oldPath)
		newName := filepath.Base(op.newPath)
		arrow := ui.StyleMuted.Render(" → ")
		fmt.Printf("  %s%s%s\n",
			ui.StylePrimary.Render(oldName),
			arrow,
			ui.StyleSecondary.Render(newName),
		)
	}

	if renameDryRun {
		fmt.Println(ui.RenderInfo(fmt.Sprintf("Dry run — %d rename(s) would be performed.", len(ops))))
		return nil
	}

	// Execute renames and collect results
	var records []tui.SummaryRecord
	for _, op := range ops {
		start := time.Now()
		renameErr := os.Rename(op.oldPath, op.newPath)
		records = append(records, tui.SummaryRecord{
			Name:     filepath.Base(op.oldPath),
			Op:       "Rename",
			Err:      renameErr,
			Size:     -1,
			Duration: time.Since(start),
		})
		if renameErr != nil {
			return fmt.Errorf("could not rename %s → %s: %w", op.oldPath, op.newPath, renameErr)
		}
	}

	if !noSummary {
		tui.ShowSummary(records)
	}

	fmt.Println(ui.RenderSuccess(fmt.Sprintf("Renamed %d file(s).", len(ops))))
	return nil
}

// computePatternRenames computes renames using glob pattern substitution
func computePatternRenames(pattern, replacement string) ([]renameOp, error) {
	// Collect matching files from current directory
	dir, globPat := filepath.Split(pattern)
	if dir == "" {
		dir = "."
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("could not read directory: %w", err)
	}

	var ops []renameOp
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		matched, err := filepath.Match(globPat, e.Name())
		if err != nil {
			return nil, err
		}
		if !matched {
			continue
		}

		newName := applyPatternRename(e.Name(), globPat, replacement)
		if newName == e.Name() {
			continue
		}

		oldPath := filepath.Join(dir, e.Name())
		newPath := filepath.Join(dir, newName)

		if _, err := os.Stat(newPath); err == nil {
			return nil, fmt.Errorf("destination already exists: %s", newPath)
		}

		ops = append(ops, renameOp{oldPath: oldPath, newPath: newPath})
	}

	return ops, nil
}

// applyPatternRename applies a glob-style rename: "*.txt" → "*.bak"
func applyPatternRename(name, pattern, replacement string) string {
	patExt := filepath.Ext(pattern)
	repExt := filepath.Ext(replacement)

	nameExt := filepath.Ext(name)
	nameBase := name[:len(name)-len(nameExt)]

	patBase := pattern[:len(pattern)-len(patExt)]
	repBase := replacement[:len(replacement)-len(repExt)]

	// Handle wildcard in base
	if patBase == "*" {
		if repBase == "*" {
			// Keep same base, change extension
			return nameBase + repExt
		}
		// Replace base entirely
		return repBase + repExt
	}

	// Handle prefix pattern like "foo_*"
	if strings.HasSuffix(patBase, "*") {
		prefix := patBase[:len(patBase)-1]
		if strings.HasPrefix(nameBase, prefix) {
			stem := nameBase[len(prefix):]
			var newBase string
			if strings.HasSuffix(repBase, "*") {
				newBase = repBase[:len(repBase)-1] + stem
			} else {
				newBase = repBase
			}
			ext := repExt
			if ext == "" {
				ext = nameExt
			}
			return newBase + ext
		}
	}

	// Simple extension change
	if nameExt == patExt && repExt != "" {
		return nameBase + repExt
	}

	return name
}

// computeRegexRenames renames files whose names match the regular expression.
// The replacement string may reference capture groups with $1, $2, or ${name}.
// Operates on files in the current working directory.
func computeRegexRenames(pattern, replacement string) ([]renameOp, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid regex: %w", err)
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		return nil, fmt.Errorf("could not read directory: %w", err)
	}

	var ops []renameOp
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !re.MatchString(name) {
			continue
		}
		newName := re.ReplaceAllString(name, replacement)
		if newName == name {
			continue
		}
		newPath := filepath.Join(".", newName)
		if _, err := os.Stat(newPath); err == nil {
			return nil, fmt.Errorf("destination already exists: %s", newName)
		}
		ops = append(ops, renameOp{oldPath: name, newPath: newPath})
	}
	return ops, nil
}

// computeSeqRenames computes sequential numbering renames
func computeSeqRenames(patterns []string) ([]renameOp, error) {
	// Collect all matching files
	var files []string
	for _, pat := range patterns {
		matches, err := filepath.Glob(pat)
		if err != nil {
			return nil, err
		}
		for _, m := range matches {
			info, err := os.Stat(m)
			if err == nil && !info.IsDir() {
				files = append(files, m)
			}
		}
	}

	if len(files) == 0 {
		return nil, nil
	}

	// Sort files for consistent ordering
	sort.Strings(files)

	ext := filepath.Ext(files[0])
	dir := filepath.Dir(files[0])

	format := fmt.Sprintf("%%0%dd%%s", renameWidth)
	var ops []renameOp

	for i, f := range files {
		n := renameStart + i
		newName := fmt.Sprintf(format, n, ext)
		newPath := filepath.Join(dir, newName)

		if f == newPath {
			continue
		}

		ops = append(ops, renameOp{oldPath: f, newPath: newPath})
	}

	return ops, nil
}

// computeCaseRenames computes case conversion renames
func computeCaseRenames(patterns []string, caseType string) ([]renameOp, error) {
	var files []string
	for _, pat := range patterns {
		matches, err := filepath.Glob(pat)
		if err != nil {
			return nil, err
		}
		for _, m := range matches {
			info, err := os.Stat(m)
			if err == nil && !info.IsDir() {
				files = append(files, m)
			}
		}
	}

	var ops []renameOp
	for _, f := range files {
		dir := filepath.Dir(f)
		base := filepath.Base(f)
		ext := filepath.Ext(base)
		nameNoExt := base[:len(base)-len(ext)]

		var newBase string
		switch strings.ToLower(caseType) {
		case "upper":
			newBase = strings.ToUpper(nameNoExt) + ext
		case "lower":
			newBase = strings.ToLower(nameNoExt) + ext
		case "title":
			newBase = toTitleCase(nameNoExt) + ext
		default:
			return nil, fmt.Errorf("unknown case type: %s (use upper, lower, or title)", caseType)
		}

		if newBase == base {
			continue
		}

		ops = append(ops, renameOp{
			oldPath: f,
			newPath: filepath.Join(dir, newBase),
		})
	}

	return ops, nil
}

// toTitleCase converts a string to Title Case
func toTitleCase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + strings.ToLower(w[1:])
		}
	}
	result := strings.Join(words, " ")

	// Also handle separators like _ and -
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == '_' || r == '-' || r == ' '
	})
	if len(parts) > 1 {
		for i, p := range parts {
			if len(p) > 0 {
				parts[i] = strings.ToUpper(p[:1]) + strings.ToLower(p[1:])
			}
		}
		// Detect separator
		sep := "_"
		if strings.Contains(s, "-") {
			sep = "-"
		} else if strings.Contains(s, " ") {
			sep = " "
		}
		result = strings.Join(parts, sep)
	}

	return result
}
