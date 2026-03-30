package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/Marin-Kitagawa/Lyra/internal/resume"
)

const (
	localBufferSize    = 32 * 1024 * 1024  // 32MB
	largeFileThreshold = 512 * 1024 * 1024 // 512MB
	goroutinePoolSize  = 8
)

// LocalOptions configures local copy behavior
type LocalOptions struct {
	Preserve    bool
	Recursive   bool
	Resume      bool
	Sync        bool
	Checksum    bool
	KeepPartial bool
}

// LocalTransfer handles local file/directory transfers
type LocalTransfer struct {
	opts     LocalOptions
	ctx      context.Context
	cancel   context.CancelFunc
	reportFn func(int64) // called with bytes written; can be nil
	doneFn   func(error) // called when done; can be nil
}

// NewLocalTransfer creates a new local transfer handler.
// reportFn is called with each chunk of bytes written (can be nil).
// doneFn is called when the transfer completes (can be nil).
func NewLocalTransfer(opts LocalOptions, reportFn func(int64), doneFn func(error)) *LocalTransfer {
	ctx, cancel := context.WithCancel(context.Background())
	return &LocalTransfer{
		opts:     opts,
		ctx:      ctx,
		cancel:   cancel,
		reportFn: reportFn,
		doneFn:   doneFn,
	}
}

// Copy copies src to dest
func (lt *LocalTransfer) Copy(src, dest string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("source does not exist: %w", err)
	}

	absSrc, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	absDest, err := filepath.Abs(dest)
	if err != nil {
		return err
	}

	if absSrc == absDest {
		return fmt.Errorf("source and destination are the same file")
	}

	var copyErr error
	if srcInfo.IsDir() {
		if !lt.opts.Recursive {
			copyErr = fmt.Errorf("source is a directory; use -r for recursive copy")
		} else {
			copyErr = lt.copyDir(absSrc, absDest)
		}
	} else {
		copyErr = lt.copyFile(absSrc, absDest, srcInfo)
	}

	if lt.doneFn != nil {
		lt.doneFn(copyErr)
	}
	return copyErr
}

// copyDir copies a directory recursively using a goroutine pool
func (lt *LocalTransfer) copyDir(src, dest string) error {
	if err := os.MkdirAll(dest, 0755); err != nil {
		return fmt.Errorf("could not create destination directory: %w", err)
	}

	type fileJob struct {
		src  string
		dest string
		info fs.FileInfo
	}

	var jobs []fileJob
	err := filepath.Walk(src, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		destPath := filepath.Join(dest, rel)

		if info.IsDir() {
			return os.MkdirAll(destPath, info.Mode())
		}

		jobs = append(jobs, fileJob{src: path, dest: destPath, info: info})
		return nil
	})
	if err != nil {
		return err
	}

	jobCh := make(chan fileJob, len(jobs))
	for _, j := range jobs {
		jobCh <- j
	}
	close(jobCh)

	var wg sync.WaitGroup
	errCh := make(chan error, goroutinePoolSize)

	workers := goroutinePoolSize
	if len(jobs) < workers {
		workers = len(jobs)
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobCh {
				select {
				case <-lt.ctx.Done():
					return
				default:
				}
				if err := lt.copyFile(job.src, job.dest, job.info); err != nil {
					errCh <- err
					return
				}
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			return err
		}
	}

	return nil
}

// copyFile copies a single file
func (lt *LocalTransfer) copyFile(src, dest string, srcInfo fs.FileInfo) error {
	if lt.opts.Sync {
		destInfo, err := os.Stat(dest)
		if err == nil {
			if destInfo.Size() == srcInfo.Size() && destInfo.ModTime().Equal(srcInfo.ModTime()) {
				return nil
			}
		}
	}

	var startOffset int64
	if lt.opts.Resume {
		state, err := resume.Load(src, dest)
		if err == nil && state != nil {
			startOffset = state.BytesDone
		}
	}

	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("could not open source file %s: %w", src, err)
	}
	defer srcFile.Close()

	if startOffset > 0 {
		if _, err := srcFile.Seek(startOffset, io.SeekStart); err != nil {
			return fmt.Errorf("could not seek source file: %w", err)
		}
	}

	var destFile *os.File
	if startOffset > 0 {
		destFile, err = os.OpenFile(dest, os.O_WRONLY|os.O_APPEND, 0644)
	} else {
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return err
		}
		destFile, err = os.Create(dest)
	}
	if err != nil {
		return fmt.Errorf("could not open destination file %s: %w", dest, err)
	}

	remaining := srcInfo.Size() - startOffset

	// For empty files, skip copy
	if remaining == 0 {
		destFile.Close()
		if lt.opts.Preserve {
			os.Chmod(dest, srcInfo.Mode())
			os.Chtimes(dest, srcInfo.ModTime(), srcInfo.ModTime())
		}
		resume.Delete(src, dest)
		return nil
	}

	var copyErr error
	if srcInfo.Size() > largeFileThreshold {
		copyErr = lt.copyLargeFile(srcFile, destFile, src, dest, startOffset, srcInfo.Size())
	} else {
		copyErr = lt.copySmall(srcFile, destFile, src, dest, startOffset, srcInfo.Size())
	}

	destFile.Close()

	if copyErr != nil {
		if !lt.opts.KeepPartial {
			os.Remove(dest)
		}
		return copyErr
	}

	if lt.opts.Preserve {
		os.Chmod(dest, srcInfo.Mode())
		os.Chtimes(dest, srcInfo.ModTime(), srcInfo.ModTime())
	}

	if lt.opts.Checksum {
		if err := ChecksumVerify(src, dest); err != nil {
			os.Remove(dest)
			return fmt.Errorf("checksum verification failed: %w", err)
		}
	}

	return nil
}

// copySmall copies a file using a simple buffered approach
func (lt *LocalTransfer) copySmall(src *os.File, dest *os.File, srcPath, destPath string, startOffset, totalSize int64) error {
	buf := make([]byte, 1024*1024)
	var written int64

	for {
		select {
		case <-lt.ctx.Done():
			state := &resume.State{
				Src:        srcPath,
				Dest:       destPath,
				BytesDone:  startOffset + written,
				TotalBytes: totalSize,
				Type:       resume.TypeLocal,
			}
			resume.Save(state)
			return fmt.Errorf("transfer paused")
		default:
		}

		n, err := src.Read(buf)
		if n > 0 {
			nw, werr := dest.Write(buf[:n])
			written += int64(nw)
			if lt.reportFn != nil {
				lt.reportFn(int64(nw))
			}
			if werr != nil {
				return fmt.Errorf("write error: %w", werr)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read error: %w", err)
		}
	}

	resume.Delete(srcPath, destPath)
	return nil
}

// pipelineChunk represents a chunk of data in the pipeline
type pipelineChunk struct {
	data []byte
	n    int
	err  error
}

// copyLargeFile copies a large file using a reader/writer pipeline
func (lt *LocalTransfer) copyLargeFile(src *os.File, dest *os.File, srcPath, destPath string, startOffset, totalSize int64) error {
	chunkCh := make(chan pipelineChunk, 4)
	doneCh := make(chan error, 1)
	var written int64

	go func() {
		defer close(chunkCh)
		buf := make([]byte, localBufferSize)
		for {
			n, err := src.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				chunkCh <- pipelineChunk{data: chunk, n: n}
			}
			if err == io.EOF {
				return
			}
			if err != nil {
				chunkCh <- pipelineChunk{err: err}
				return
			}
		}
	}()

	go func() {
		for chunk := range chunkCh {
			if chunk.err != nil {
				doneCh <- chunk.err
				return
			}
			select {
			case <-lt.ctx.Done():
				state := &resume.State{
					Src:        srcPath,
					Dest:       destPath,
					BytesDone:  startOffset + written,
					TotalBytes: totalSize,
					Type:       resume.TypeLocal,
				}
				resume.Save(state)
				doneCh <- fmt.Errorf("transfer paused")
				return
			default:
			}

			nw, err := dest.Write(chunk.data[:chunk.n])
			written += int64(nw)
			if lt.reportFn != nil {
				lt.reportFn(int64(nw))
			}
			if err != nil {
				doneCh <- fmt.Errorf("write error: %w", err)
				return
			}
		}
		doneCh <- nil
	}()

	if err := <-doneCh; err != nil {
		return err
	}

	resume.Delete(srcPath, destPath)
	return nil
}

// Cancel cancels the transfer
func (lt *LocalTransfer) Cancel() {
	lt.cancel()
}

// ChecksumVerify verifies the SHA256 checksums of src and dest match
func ChecksumVerify(src, dest string) error {
	srcHash, err := SHA256File(src)
	if err != nil {
		return fmt.Errorf("could not hash source: %w", err)
	}
	destHash, err := SHA256File(dest)
	if err != nil {
		return fmt.Errorf("could not hash destination: %w", err)
	}
	if srcHash != destHash {
		return fmt.Errorf("checksum mismatch: source=%s, dest=%s", srcHash, destHash)
	}
	return nil
}

// SHA256File computes the SHA256 hash of a file
func SHA256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	buf := make([]byte, 1024*1024)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
