package nzbfilesystem

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/javi11/altmount/internal/sevenzip"
	"github.com/spf13/afero"
)

// StreamableArchiveFile implements afero.File for a streamable file inside a 7z archive.
type StreamableArchiveFile struct {
	archiveReader io.ReaderAt
	fileEntry     sevenzip.FileEntry
	position      int64
	reader        io.ReadCloser
	mu            sync.Mutex
}

// NewStreamableArchiveFile creates a new virtual file for a streamable file inside a 7z archive.
func NewStreamableArchiveFile(archiveReader io.ReaderAt, fe sevenzip.FileEntry) (afero.File, error) {
	return &StreamableArchiveFile{
		archiveReader: archiveReader,
		fileEntry:     fe,
	}, nil
}

func (f *StreamableArchiveFile) Read(p []byte) (n int, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.reader == nil {
		if err := f.ensureReader(); err != nil {
			return 0, err
		}
	}

	n, err = f.reader.Read(p)
	f.position += int64(n)
	return n, err
}

func (f *StreamableArchiveFile) ReadAt(p []byte, off int64) (n int, err error) {
	sectionReader := io.NewSectionReader(f.archiveReader, int64(f.fileEntry.Offset), int64(f.fileEntry.Size))
	return sectionReader.ReadAt(p, off)
}

func (f *StreamableArchiveFile) Seek(offset int64, whence int) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = f.position + offset
	case io.SeekEnd:
		abs = int64(f.fileEntry.Size) + offset
	default:
		return 0, os.ErrInvalid
	}

	if abs < 0 {
		return 0, os.ErrInvalid
	}

	f.position = abs
	if f.reader != nil {
		f.reader.Close()
		f.reader = nil
	}

	return abs, nil
}

func (f *StreamableArchiveFile) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.reader != nil {
		err := f.reader.Close()
		f.reader = nil
		return err
	}
	return nil
}

func (f *StreamableArchiveFile) Name() string {
	return filepath.Base(f.fileEntry.Name)
}

func (f *StreamableArchiveFile) Stat() (fs.FileInfo, error) {
	return &MetadataFileInfo{
		name:    filepath.Base(f.fileEntry.Name),
		size:    int64(f.fileEntry.Size),
		modTime: f.fileEntry.Modified,
		isDir:   false,
	}, nil
}

func (f *StreamableArchiveFile) ensureReader() error {
	sectionReader, err := sevenzip.Extract(f.archiveReader, f.fileEntry)
	if err != nil {
		return err
	}

	if f.position > 0 {
		if _, err := sectionReader.Seek(f.position, io.SeekStart); err != nil {
			return err
		}
	}

	// The section reader from Extract does not need closing, as the underlying
	// archiveReader is managed separately and does not need to be closed.
	f.reader = io.NopCloser(sectionReader)

	return nil
}

func (f *StreamableArchiveFile) Readdir(count int) ([]fs.FileInfo, error) {
	return nil, ErrNotDirectory
}

func (f *StreamableArchiveFile) Readdirnames(n int) ([]string, error) {
	return nil, ErrNotDirectory
}

func (f *StreamableArchiveFile) Sync() error {
	return nil
}

func (f *StreamableArchiveFile) Truncate(size int64) error {
	return os.ErrPermission
}

func (f *StreamableArchiveFile) Write(p []byte) (n int, err error) {
	return 0, os.ErrPermission
}

func (f *StreamableArchiveFile) WriteAt(p []byte, off int64) (n int, err error) {
	return 0, os.ErrPermission
}

func (f *StreamableArchiveFile) WriteString(s string) (ret int, err error) {
	return 0, os.ErrPermission
}
