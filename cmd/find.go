package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/lyra-cli/lyra/internal/tui"
	"github.com/lyra-cli/lyra/internal/ui"
	"github.com/spf13/cobra"
)

var (
	findName     string
	findSize     string
	findModified string
	findType     string
	findMaxDepth int
	findRegex    bool
)

var findCmd = &cobra.Command{
	Use:   "find [path]",
	Short: "Find files with pattern matching",
	Long: `Find files and directories with flexible filtering.

Examples:
  lyra find . --name "*.go"
  lyra find . --name "^main\.(go|js)$" --regex
  lyra find . --size +1GB
  lyra find . --size -100KB
  lyra find . --modified "last 7 days"
  lyra find . --type dir
  lyra find . --type file --name "*.log"
  lyra find . --regex --name "test_\d+"`,
	Args: cobra.MaximumNArgs(1),
	RunE: runFind,
}

func init() {
	findCmd.Flags().StringVarP(&findName, "name", "n", "", "Name pattern (glob by default; regex with --regex)")
	findCmd.Flags().StringVarP(&findSize, "size", "s", "", "Size filter (+1GB, -100KB, 50MB)")
	findCmd.Flags().StringVarP(&findModified, "modified", "m", "", "Modified time filter (e.g. 'last 7 days')")
	findCmd.Flags().StringVarP(&findType, "type", "t", "", "Type filter: file, dir, symlink")
	findCmd.Flags().IntVar(&findMaxDepth, "max-depth", -1, "Maximum depth (-1 for unlimited)")
	findCmd.Flags().BoolVarP(&findRegex, "regex", "e", false, "Treat --name as a regular expression")
}

// sizeFilter represents a parsed size filter
type sizeFilter struct {
	op   byte // '+', '-', or '='
	size int64
}

// parseSizeFilter parses +1GB, -100KB, 50MB etc.
func parseSizeFilter(s string) (*sizeFilter, error) {
	if s == "" {
		return nil, nil
	}

	f := &sizeFilter{op: '='}
	s = strings.TrimSpace(s)

	if s[0] == '+' {
		f.op = '+'
		s = s[1:]
	} else if s[0] == '-' {
		f.op = '-'
		s = s[1:]
	}

	// Parse size
	var value int64
	var unit string
	_, err := fmt.Sscanf(s, "%d%s", &value, &unit)
	if err != nil {
		// Try just a number
		_, err = fmt.Sscanf(s, "%d", &value)
		if err != nil {
			return nil, fmt.Errorf("invalid size: %s", s)
		}
		f.size = value
		return f, nil
	}

	multiplier := int64(1)
	switch strings.ToUpper(unit) {
	case "B":
		multiplier = 1
	case "KB", "K":
		multiplier = 1024
	case "MB", "M":
		multiplier = 1024 * 1024
	case "GB", "G":
		multiplier = 1024 * 1024 * 1024
	case "TB", "T":
		multiplier = 1024 * 1024 * 1024 * 1024
	default:
		return nil, fmt.Errorf("unknown size unit: %s", unit)
	}

	f.size = value * multiplier
	return f, nil
}

// matchesSize checks if a file matches the size filter
func (f *sizeFilter) matches(size int64) bool {
	switch f.op {
	case '+':
		return size > f.size
	case '-':
		return size < f.size
	default:
		return size == f.size
	}
}

// parseModifiedFilter parses time filters like "last 7 days"
func parseModifiedFilter(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}

	s = strings.ToLower(strings.TrimSpace(s))

	if strings.HasPrefix(s, "last ") {
		rest := strings.TrimPrefix(s, "last ")
		var n int
		var unit string
		_, err := fmt.Sscanf(rest, "%d %s", &n, &unit)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid time filter: %s", s)
		}

		now := time.Now()
		switch strings.TrimSuffix(unit, "s") {
		case "minute":
			return now.Add(-time.Duration(n) * time.Minute), nil
		case "hour":
			return now.Add(-time.Duration(n) * time.Hour), nil
		case "day":
			return now.AddDate(0, 0, -n), nil
		case "week":
			return now.AddDate(0, 0, -n*7), nil
		case "month":
			return now.AddDate(0, -n, 0), nil
		case "year":
			return now.AddDate(-n, 0, 0), nil
		default:
			return time.Time{}, fmt.Errorf("unknown time unit: %s", unit)
		}
	}

	return time.Time{}, fmt.Errorf("invalid time filter format: %s (try 'last 7 days')", s)
}

func runFind(cmd *cobra.Command, args []string) error {
	searchPath := "."
	if len(args) > 0 {
		searchPath = args[0]
	}

	sizef, err := parseSizeFilter(findSize)
	if err != nil {
		return err
	}

	var modifiedAfter time.Time
	if findModified != "" {
		modifiedAfter, err = parseModifiedFilter(findModified)
		if err != nil {
			return err
		}
	}

	// Compile regex up front so a bad pattern is rejected before the spinner.
	var nameRe *regexp.Regexp
	if findName != "" && findRegex {
		nameRe, err = regexp.Compile(findName)
		if err != nil {
			return fmt.Errorf("invalid --name regex: %w", err)
		}
	}

	label := fmt.Sprintf("Searching in %s…", ui.StylePrimary.Render(searchPath))
	tui.RunWithSpinner(label, func() string {
		return doFind(searchPath, sizef, modifiedAfter, nameRe)
	})
	return nil
}

// doFind performs the actual walk and returns a fully-rendered result string.
// nameRe is non-nil only when --regex is set; otherwise glob matching is used.
func doFind(searchPath string, sizef *sizeFilter, modifiedAfter time.Time, nameRe *regexp.Regexp) string {
	baseDepth := strings.Count(filepath.Clean(searchPath), string(os.PathSeparator))
	var lines []string

	filepath.Walk(searchPath, func(path string, info os.FileInfo, err error) error { //nolint:errcheck
		if err != nil || path == searchPath {
			return nil
		}

		if findMaxDepth >= 0 {
			depth := strings.Count(filepath.Clean(path), string(os.PathSeparator)) - baseDepth
			if depth > findMaxDepth {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}

		if findType != "" {
			switch findType {
			case "file":
				if !info.Mode().IsRegular() {
					return nil
				}
			case "dir", "directory":
				if !info.IsDir() {
					return nil
				}
			case "symlink", "link":
				if info.Mode()&os.ModeSymlink == 0 {
					return nil
				}
			}
		}

		if findName != "" {
			base := filepath.Base(path)
			var matched bool
			if nameRe != nil {
				matched = nameRe.MatchString(base)
			} else {
				var merr error
				matched, merr = filepath.Match(findName, base)
				if merr != nil {
					return nil
				}
			}
			if !matched {
				return nil
			}
		}

		if sizef != nil && (info.IsDir() || !sizef.matches(info.Size())) {
			return nil
		}

		if !modifiedAfter.IsZero() && info.ModTime().Before(modifiedAfter) {
			return nil
		}

		lines = append(lines, buildFindResult(path, info))
		return nil
	})

	var sb strings.Builder
	sb.WriteString("\n")
	for _, l := range lines {
		sb.WriteString(l + "\n")
	}
	sb.WriteString("\n")
	sb.WriteString(ui.StyleMuted.Render(fmt.Sprintf("  Found %d item(s)", len(lines))))
	sb.WriteString("\n")
	return sb.String()
}

func buildFindResult(path string, info os.FileInfo) string {
	icon := fileIcon(info)
	style := fileStyle(info)
	extra := ""
	if !info.IsDir() {
		extra = ui.StyleMuted.Render(fmt.Sprintf(" (%s)", humanize.Bytes(uint64(info.Size()))))
	}
	return fmt.Sprintf("  %s %s%s", icon, style.Render(path), extra)
}
