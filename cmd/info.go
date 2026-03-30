package cmd

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/dustin/go-humanize"
	"github.com/lyra-cli/lyra/internal/tui"
	"github.com/lyra-cli/lyra/internal/ui"
	"github.com/spf13/cobra"
)

var infoCmd = &cobra.Command{
	Use:   "info <file>",
	Short: "Show detailed file information",
	Long: `Show detailed information about a file including checksums, MIME type, and permissions.

Examples:
  lyra info file.txt
  lyra info image.png`,
	Args: cobra.ExactArgs(1),
	RunE: runInfo,
}

func runInfo(cmd *cobra.Command, args []string) error {
	path := args[0]

	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("cannot access %s: %w", path, err)
	}

	// Checksum computation can be slow for large files — run inside a spinner.
	label := fmt.Sprintf("Analysing %s…", ui.StylePrimary.Render(info.Name()))
	tui.RunWithSpinner(label, func() string {
		return buildInfoOutput(path, info)
	})
	return nil
}

// buildInfoOutput gathers all file metadata and returns the fully-rendered string.
func buildInfoOutput(path string, info os.FileInfo) string {
	var sb strings.Builder
	icon := fileIcon(info)
	sb.WriteString("\n")
	sb.WriteString(ui.RenderHeader(fmt.Sprintf("%s  %s", icon, info.Name())) + "\n\n")

	lines := []string{
		ui.RenderKeyValue("Path", path),
		ui.RenderKeyValue("Type", fileType(info)),
		ui.RenderKeyValue("Size", fmt.Sprintf("%s (%d bytes)", humanize.Bytes(uint64(info.Size())), info.Size())),
		ui.RenderKeyValue("Mode", info.Mode().String()),
		ui.RenderKeyValue("Modified", info.ModTime().Format("2006-01-02 15:04:05")),
	}

	if !info.IsDir() {
		mimeType, err := detectMIME(path)
		if err == nil {
			lines = append(lines, ui.RenderKeyValue("MIME Type", mimeType))
		}
		sb.WriteString(ui.RenderInfoBox(strings.Join(lines, "\n")) + "\n\n")
		sb.WriteString(ui.StylePrimary.Bold(true).Render("Checksums:") + "\n\n")
		hashLines := computeHashes(path)
		sb.WriteString(ui.RenderInfoBox(strings.Join(hashLines, "\n")) + "\n\n")
		if dims, err := getImageDimensions(path, mimeType); err == nil && dims != "" {
			sb.WriteString(ui.RenderKeyValue("Dimensions", dims) + "\n")
		}
	} else {
		count, size, err := dirStats(path)
		if err == nil {
			lines = append(lines, ui.RenderKeyValue("Contents", fmt.Sprintf("%d items", count)))
			lines = append(lines, ui.RenderKeyValue("Total Size", humanize.Bytes(uint64(size))))
		}
		sb.WriteString(ui.RenderInfoBox(strings.Join(lines, "\n")) + "\n\n")
	}
	return sb.String()
}

func fileType(info os.FileInfo) string {
	switch {
	case info.IsDir():
		return "Directory"
	case info.Mode()&os.ModeSymlink != 0:
		return "Symbolic Link"
	case info.Mode()&os.ModeDevice != 0:
		return "Device"
	case info.Mode()&os.ModeNamedPipe != 0:
		return "Named Pipe"
	case info.Mode()&os.ModeSocket != 0:
		return "Socket"
	case info.Mode()&0111 != 0:
		return "Executable File"
	default:
		return "Regular File"
	}
}

func detectMIME(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return "", err
	}

	return http.DetectContentType(buf[:n]), nil
}

func computeHashes(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return []string{ui.RenderError("Could not open file for hashing")}
	}
	defer f.Close()

	md5h := md5.New()
	sha1h := sha1.New()
	sha256h := sha256.New()

	writers := io.MultiWriter(md5h, sha1h, sha256h)

	buf := make([]byte, 1024*1024)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			writers.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return []string{ui.RenderError(fmt.Sprintf("Hash error: %v", err))}
		}
	}

	return []string{
		ui.RenderKeyValue("MD5", hex.EncodeToString(md5h.Sum(nil))),
		ui.RenderKeyValue("SHA1", hex.EncodeToString(sha1h.Sum(nil))),
		ui.RenderKeyValue("SHA256", hex.EncodeToString(sha256h.Sum(nil))),
	}
}

func getImageDimensions(path, mimeType string) (string, error) {
	if !strings.HasPrefix(mimeType, "image/") {
		return "", fmt.Errorf("not an image")
	}

	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	// Read PNG dimensions from header
	if strings.Contains(mimeType, "png") {
		buf := make([]byte, 24)
		if n, err := f.Read(buf); n >= 24 && err == nil {
			if buf[0] == 137 && buf[1] == 80 && buf[2] == 78 && buf[3] == 71 {
				width := int(buf[16])<<24 | int(buf[17])<<16 | int(buf[18])<<8 | int(buf[19])
				height := int(buf[20])<<24 | int(buf[21])<<16 | int(buf[22])<<8 | int(buf[23])
				return fmt.Sprintf("%d × %d", width, height), nil
			}
		}
	}

	// Read JPEG dimensions
	if strings.Contains(mimeType, "jpeg") {
		return readJPEGDimensions(f)
	}

	return "", fmt.Errorf("could not read dimensions")
}

func readJPEGDimensions(f *os.File) (string, error) {
	f.Seek(0, io.SeekStart)
	buf := make([]byte, 2)
	if _, err := io.ReadFull(f, buf); err != nil {
		return "", err
	}
	if buf[0] != 0xFF || buf[1] != 0xD8 {
		return "", fmt.Errorf("not a valid JPEG")
	}

	tmp := make([]byte, 4)
	for {
		if _, err := io.ReadFull(f, tmp[:2]); err != nil {
			return "", err
		}
		if tmp[0] != 0xFF {
			return "", fmt.Errorf("invalid JPEG marker")
		}
		marker := tmp[1]

		if _, err := io.ReadFull(f, tmp[:2]); err != nil {
			return "", err
		}
		length := int(tmp[0])<<8 | int(tmp[1])

		// SOF markers
		if marker >= 0xC0 && marker <= 0xC3 {
			sof := make([]byte, length-2)
			if _, err := io.ReadFull(f, sof); err != nil {
				return "", err
			}
			height := int(sof[1])<<8 | int(sof[2])
			width := int(sof[3])<<8 | int(sof[4])
			return fmt.Sprintf("%d × %d", width, height), nil
		}

		if _, err := f.Seek(int64(length-2), io.SeekCurrent); err != nil {
			return "", err
		}
	}
}

func dirStats(path string) (int, int64, error) {
	var count int
	var size int64

	err := walkDir(path, func(p string, info os.FileInfo) {
		if !info.IsDir() {
			count++
			size += info.Size()
		}
	})

	return count, size, err
}

func walkDir(path string, fn func(string, os.FileInfo)) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}

	for _, e := range entries {
		fullPath := path + string(os.PathSeparator) + e.Name()
		info, err := e.Info()
		if err != nil {
			continue
		}
		fn(fullPath, info)
		if e.IsDir() {
			walkDir(fullPath, fn)
		}
	}
	return nil
}
