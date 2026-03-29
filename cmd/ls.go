package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/dustin/go-humanize"
	"github.com/lyra-cli/lyra/internal/ui"
	"github.com/spf13/cobra"
)

var (
	lsAll  bool
	lsTree bool
	lsSort string
)

// Column widths in visible terminal cells
const (
	colNameWidth = 26
	colSizeWidth = 10
)

var lsCmd = &cobra.Command{
	Use:   "ls [path]",
	Short: "Beautiful directory listing",
	Long: `List directory contents with colors, icons, and human-readable sizes.

Examples:
  lyra ls
  lyra ls /tmp
  lyra ls --all         # Show hidden files
  lyra ls --tree        # Tree view
  lyra ls --sort size   # Sort by size
  lyra ls --sort time   # Sort by modification time
  lyra ls --sort type   # Sort by file type`,
	Args: cobra.MaximumNArgs(1),
	RunE: runLs,
}

func init() {
	lsCmd.Flags().BoolVarP(&lsAll, "all", "a", false, "Show hidden files")
	lsCmd.Flags().BoolVar(&lsTree, "tree", false, "Show tree view")
	lsCmd.Flags().StringVar(&lsSort, "sort", "name", "Sort order: name, size, time, type")
}

// stripANSI removes ANSI/VT escape sequences from s using a simple state machine.
func stripANSI(s string) string {
	out := make([]byte, 0, len(s))
	inEsc := false
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b == '\x1b' {
			inEsc = true
			continue
		}
		if inEsc {
			// Escape sequence ends at a letter (m, K, H, etc.)
			if (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') {
				inEsc = false
			}
			continue
		}
		out = append(out, b)
	}
	return string(out)
}

// runeDisplayWidth returns how many terminal columns a rune occupies.
// Emoji and CJK characters are 2 columns wide; most others are 1.
func runeDisplayWidth(r rune) int {
	if r < 0x1100 {
		return 1
	}
	switch {
	case r >= 0x1100 && r <= 0x115F,  // Hangul Jamo
		r == 0x2329 || r == 0x232A,
		r >= 0x2E80 && r <= 0x303E,   // CJK Radicals / Kangxi
		r >= 0x3040 && r <= 0x33FF,   // Hiragana, Katakana, CJK symbols
		r >= 0x3400 && r <= 0x4DBF,   // CJK Extension A
		r >= 0x4E00 && r <= 0x9FFF,   // CJK Unified Ideographs
		r >= 0xA000 && r <= 0xA4CF,   // Yi Syllables
		r >= 0xAC00 && r <= 0xD7AF,   // Hangul Syllables
		r >= 0xF900 && r <= 0xFAFF,   // CJK Compatibility Ideographs
		r >= 0xFE10 && r <= 0xFE19,   // Vertical forms
		r >= 0xFE30 && r <= 0xFE6F,   // CJK Compatibility Forms
		r >= 0xFF00 && r <= 0xFF60,   // Fullwidth Forms
		r >= 0xFFE0 && r <= 0xFFE6,   // Fullwidth Signs
		r >= 0x1B000 && r <= 0x1B77F, // Kana Supplement
		r >= 0x1F004 && r <= 0x1FFFF, // Emoji, Mahjong, Misc Symbols
		r >= 0x20000 && r <= 0x2FFFD, // CJK Extension B–F
		r >= 0x30000 && r <= 0x3FFFD: // CJK Extension G
		return 2
	}
	return 1
}

// visibleWidth returns the number of terminal columns occupied by s,
// correctly handling ANSI escape codes and wide (emoji/CJK) characters.
func visibleWidth(s string) int {
	plain := stripANSI(s)
	w := 0
	for _, r := range plain {
		w += runeDisplayWidth(r)
	}
	return w
}

// padRight pads s so that its visible terminal width equals exactly `width`.
// If s is already wider, it is returned unchanged.
func padRight(s string, width int) string {
	vw := visibleWidth(s)
	if vw >= width {
		return s
	}
	return s + strings.Repeat(" ", width-vw)
}

// fileIcon returns a single emoji icon for the given file.
func fileIcon(info os.FileInfo) string {
	if info.IsDir() {
		return "📁"
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "🔗"
	}

	name := strings.ToLower(info.Name())
	ext := filepath.Ext(name)

	switch ext {
	case ".go":
		return "🐹"
	case ".js", ".ts", ".jsx", ".tsx":
		return "📜"
	case ".py":
		return "🐍"
	case ".rs":
		return "🦀"
	case ".rb":
		return "💎"
	case ".java":
		return "☕"
	case ".c", ".cpp", ".cc", ".h", ".hpp":
		return "⚙️"
	case ".sh", ".bash", ".zsh", ".fish":
		return "🐚"
	case ".md", ".markdown":
		return "📝"
	case ".json", ".yaml", ".yml", ".toml":
		return "🔧"
	case ".html", ".htm":
		return "🌐"
	case ".css", ".scss", ".sass":
		return "🎨"
	case ".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp":
		return "🖼️"
	case ".mp3", ".wav", ".flac", ".ogg", ".m4a":
		return "🎵"
	case ".mp4", ".mov", ".avi", ".mkv", ".webm":
		return "🎬"
	case ".pdf":
		return "📄"
	case ".zip", ".tar", ".gz", ".bz2", ".xz", ".7z", ".rar":
		return "📦"
	case ".exe", ".msi", ".dmg", ".deb", ".rpm":
		return "⚡"
	case ".txt":
		return "📃"
	case ".csv":
		return "📊"
	case ".log":
		return "📋"
	case ".sql", ".db", ".sqlite":
		return "🗄️"
	case ".lock":
		return "🔒"
	case ".env":
		return "🔐"
	}

	if info.Mode()&0111 != 0 {
		return "⚡"
	}
	return "📄"
}

// fileStyle returns the lipgloss style for a file entry.
func fileStyle(info os.FileInfo) lipgloss.Style {
	if info.IsDir() {
		return lipgloss.NewStyle().Foreground(ui.ColorAccent).Bold(true)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return lipgloss.NewStyle().Foreground(ui.ColorSecondary)
	}
	if info.Mode()&0111 != 0 {
		return lipgloss.NewStyle().Foreground(ui.ColorSuccess)
	}
	if strings.HasPrefix(info.Name(), ".") {
		return lipgloss.NewStyle().Foreground(ui.ColorMuted)
	}
	return lipgloss.NewStyle().Foreground(ui.ColorWhite)
}

func runLs(cmd *cobra.Command, args []string) error {
	path := "."
	if len(args) > 0 {
		path = args[0]
	}

	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("cannot access %s: %w", path, err)
	}

	if !info.IsDir() {
		printEntry(path, info, "")
		return nil
	}

	if lsTree {
		fmt.Println(ui.StylePrimary.Bold(true).Render(path))
		return printTree(path, "", lsAll)
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("could not read directory: %w", err)
	}

	var fileInfos []os.FileInfo
	for _, e := range entries {
		if !lsAll && strings.HasPrefix(e.Name(), ".") {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		fileInfos = append(fileInfos, fi)
	}

	sortEntries(fileInfos, lsSort)

	// Header
	// Layout (visual columns):
	//   prefix(2) + iconPlaceholder(2) + space(1) + Name(colNameWidth) + space(1) + Size(colSizeWidth) + space(1) + Modified
	headerStyle := lipgloss.NewStyle().Foreground(ui.ColorMuted).Italic(true)
	headerLine := "  " + "  " + " " +
		padRight(headerStyle.Render("Name"), colNameWidth) + " " +
		padRight(headerStyle.Render("Size"), colSizeWidth) + " " +
		headerStyle.Render("Modified")
	sepLine := "  " + ui.StyleMuted.Render(strings.Repeat("─", 2+1+colNameWidth+1+colSizeWidth+1+14))

	fmt.Println(headerLine)
	fmt.Println(sepLine)

	for _, fi := range fileInfos {
		printEntry(path, fi, "  ")
	}

	fmt.Printf("\n%s\n", ui.StyleMuted.Render(fmt.Sprintf("  %d item%s", len(fileInfos), pluralS(len(fileInfos)))))
	return nil
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func printEntry(dir string, info os.FileInfo, prefix string) {
	icon := fileIcon(info)
	style := fileStyle(info)
	name := style.Render(info.Name())

	var sizeStr string
	if info.IsDir() {
		sizeStr = ui.StyleMuted.Render("—")
	} else {
		sizeStr = ui.StyleSecondary.Render(humanize.Bytes(uint64(info.Size())))
	}

	modTime := ui.StyleMuted.Render(formatModTime(info.ModTime()))

	var linkTarget string
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(filepath.Join(dir, info.Name()))
		if err == nil {
			linkTarget = ui.StyleMuted.Render(" → " + target)
		}
	}

	// All padding is done via visibleWidth-aware padRight, so ANSI codes
	// in name/sizeStr do not break column alignment.
	line := prefix +
		icon + " " +
		padRight(name, colNameWidth) + " " +
		padRight(sizeStr, colSizeWidth) + " " +
		modTime +
		linkTarget

	fmt.Println(line)
}

func formatModTime(t time.Time) string {
	now := time.Now()
	if now.Year() == t.Year() {
		return t.Format("Jan 02 15:04")
	}
	return t.Format("Jan 02  2006")
}

func sortEntries(entries []os.FileInfo, by string) {
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		// Directories always first
		if a.IsDir() != b.IsDir() {
			return a.IsDir()
		}
		switch by {
		case "size":
			return a.Size() > b.Size()
		case "time":
			return a.ModTime().After(b.ModTime())
		case "type":
			extA := filepath.Ext(a.Name())
			extB := filepath.Ext(b.Name())
			if extA != extB {
				return extA < extB
			}
			return strings.ToLower(a.Name()) < strings.ToLower(b.Name())
		default: // "name"
			return strings.ToLower(a.Name()) < strings.ToLower(b.Name())
		}
	})
}

func printTree(path, indent string, showHidden bool) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}

	var visible []os.DirEntry
	for _, e := range entries {
		if !showHidden && strings.HasPrefix(e.Name(), ".") {
			continue
		}
		visible = append(visible, e)
	}

	for i, e := range visible {
		isLast := i == len(visible)-1
		connector := "├── "
		if isLast {
			connector = "└── "
		}

		fi, err := e.Info()
		if err != nil {
			continue
		}

		icon := fileIcon(fi)
		style := fileStyle(fi)
		name := style.Render(e.Name())

		fmt.Printf("%s%s%s %s\n", indent, ui.StyleMuted.Render(connector), icon, name)

		if e.IsDir() {
			childIndent := indent
			if isLast {
				childIndent += "    "
			} else {
				childIndent += ui.StyleMuted.Render("│") + "   "
			}
			if err := printTree(filepath.Join(path, e.Name()), childIndent, showHidden); err != nil {
				return err
			}
		}
	}
	return nil
}
