package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/lyra-cli/lyra/internal/ui"
	"github.com/spf13/cobra"
)

var (
	findName     string
	findSize     string
	findModified string
	findType     string
	findMaxDepth int
)

var findCmd = &cobra.Command{
	Use:   "find [path]",
	Short: "Find files with pattern matching",
	Long: `Find files and directories with flexible filtering.

Examples:
  lyra find . -name "*.go"
  lyra find . -size +1GB
  lyra find . -size -100KB
  lyra find . -modified "last 7 days"
  lyra find . -type dir
  lyra find . -type file -name "*.log"`,
	Args: cobra.MaximumNArgs(1),
	RunE: runFind,
}

func init() {
	findCmd.Flags().StringVarP(&findName, "name", "n", "", "Name pattern (glob, e.g. '*.go')")
	findCmd.Flags().StringVarP(&findSize, "size", "s", "", "Size filter (+1GB, -100KB, 50MB)")
	findCmd.Flags().StringVarP(&findModified, "modified", "m", "", "Modified time filter (e.g. 'last 7 days')")
	findCmd.Flags().StringVarP(&findType, "type", "t", "", "Type filter: file, dir, symlink")
	findCmd.Flags().IntVar(&findMaxDepth, "max-depth", -1, "Maximum depth (-1 for unlimited)")
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

	baseDepth := strings.Count(filepath.Clean(searchPath), string(os.PathSeparator))
	found := 0

	err = filepath.Walk(searchPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}

		// Depth check
		if findMaxDepth >= 0 {
			currentDepth := strings.Count(filepath.Clean(path), string(os.PathSeparator)) - baseDepth
			if currentDepth > findMaxDepth {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}

		// Skip root
		if path == searchPath {
			return nil
		}

		// Type filter
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

		// Name pattern filter
		if findName != "" {
			matched, err := filepath.Match(findName, filepath.Base(path))
			if err != nil || !matched {
				return nil
			}
		}

		// Size filter
		if sizef != nil {
			if info.IsDir() || !sizef.matches(info.Size()) {
				return nil
			}
		}

		// Modified time filter
		if !modifiedAfter.IsZero() {
			if info.ModTime().Before(modifiedAfter) {
				return nil
			}
		}

		// Print result
		printFindResult(path, info)
		found++
		return nil
	})

	if err != nil {
		return err
	}

	fmt.Printf("\n%s\n", ui.StyleMuted.Render(fmt.Sprintf("Found %d item(s)", found)))
	return nil
}

func printFindResult(path string, info os.FileInfo) {
	icon := fileIcon(info)
	style := fileStyle(info)

	var extra string
	if !info.IsDir() {
		extra = ui.StyleMuted.Render(fmt.Sprintf(" (%s)", humanize.Bytes(uint64(info.Size()))))
	}

	fmt.Printf("%s %s%s\n", icon, style.Render(path), extra)
}
