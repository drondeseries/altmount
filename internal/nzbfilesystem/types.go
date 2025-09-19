package nzbfilesystem

import (
	"io"
	"io/fs"
	"os"
	"strings"
	"sync"
	"time"
)

// normalizePath normalizes file paths for consistent database lookups
// Removes trailing slashes except for root path "/"
func normalizePath(path string) string {
	// Handle empty path
	if path == "" {
		return RootPath
	}

	// Handle root path - keep as is
	if path == RootPath {
		return path
	}

	// Remove trailing slashes for all other paths
	return strings.TrimRight(path, "/")
}

type StreamedVirtualFile struct {
	name       string
	size       int64
	modTime    time.Time
	isDir      bool
	reader     io.ReadCloser
	readSeeker io.ReadSeeker
	position   int64
	mu         sync.Mutex
}

func (svf *StreamedVirtualFile) Close() error {
	return svf.reader.Close()
}

func (svf *StreamedVirtualFile) Read(p []byte) (n int, err error) {
	svf.mu.Lock()
	defer svf.mu.Unlock()
	n, err = svf.reader.Read(p)
	svf.position += int64(n)
	return n, err
}

func (svf *StreamedVirtualFile) ReadAt(p []byte, off int64) (n int, err error) {
	svf.mu.Lock()
	defer svf.mu.Unlock()
	if _, err := svf.readSeeker.Seek(off, io.SeekStart); err != nil {
		return 0, err
	}
	n, err = svf.reader.Read(p)
	svf.position = off + int64(n)
	return n, err
}

func (svf *StreamedVirtualFile) Seek(offset int64, whence int) (int64, error) {
	svf.mu.Lock()
	defer svf.mu.Unlock()
	newPos, err := svf.readSeeker.Seek(offset, whence)
	if err == nil {
		svf.position = newPos
	}
	return newPos, err
}

func (svf *StreamedVirtualFile) Write(p []byte) (n int, err error) {
	return 0, os.ErrPermission
}

func (svf *StreamedVirtualFile) WriteAt(p []byte, off int64) (n int, err error) {
	return 0, os.ErrPermission
}

func (svf *StreamedVirtualFile) Name() string {
	return svf.name
}

func (svf *StreamedVirtualFile) Size() int64 {
	return svf.size
}

func (svf *StreamedVirtualFile) Mode() os.FileMode {
	if svf.isDir {
		return os.ModeDir | 0755
	}
	return 0644
}

func (svf *StreamedVirtualFile) ModTime() time.Time {
	return svf.modTime
}

func (svf *StreamedVirtualFile) IsDir() bool {
	return svf.isDir
}

func (svf *StreamedVirtualFile) Sys() interface{} {
	return nil
}

func (svf *StreamedVirtualFile) Stat() (fs.FileInfo, error) {
	return svf, nil
}

func (svf *StreamedVirtualFile) Truncate(size int64) error {
	return os.ErrPermission
}

func (svf *StreamedVirtualFile) WriteString(s string) (n int, err error) {
	return 0, os.ErrPermission
}

func (svf *StreamedVirtualFile) Readdir(count int) ([]fs.FileInfo, error) {
	return nil, ErrNotDirectory
}

func (svf *StreamedVirtualFile) Readdirnames(n int) ([]string, error) {
	return nil, ErrNotDirectory
}

func (svf *StreamedVirtualFile) Sync() error {
	return nil
}
