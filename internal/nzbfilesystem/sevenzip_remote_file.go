package nzbfilesystem

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/bodgit/sevenzip"
	"github.com/javi11/altmount/internal/config"
	"github.com/javi11/altmount/internal/importer"
	metapb "github.com/javi11/altmount/internal/metadata/proto"
	"github.com/javi11/altmount/internal/pool"
	"github.com/spf13/afero"
)

// SevenZipVirtualFile implements afero.File for files inside a 7z archive
type SevenZipVirtualFile struct {
	name         string
	fileMeta     *metapb.FileMetadata
	poolManager  pool.Manager
	configGetter config.ConfigGetter

	ctx context.Context
	mu  sync.Mutex

	reader            io.ReadCloser
	readerInitialized bool
	position          int64
}

// NewSevenZipVirtualFile creates a new virtual file for a file inside a 7z archive
func NewSevenZipVirtualFile(
	ctx context.Context,
	name string,
	fileMeta *metapb.FileMetadata,
	poolManager pool.Manager,
	configGetter config.ConfigGetter,
) (afero.File, error) {
	return &SevenZipVirtualFile{
		name:         name,
		fileMeta:     fileMeta,
		poolManager:  poolManager,
		configGetter: configGetter,
		ctx:          ctx,
	}, nil
}

// Read implements afero.File.Read
func (svf *SevenZipVirtualFile) Read(p []byte) (n int, err error) {
	svf.mu.Lock()
	defer svf.mu.Unlock()

	if err := svf.ensureReader(); err != nil {
		return 0, err
	}

	n, err = svf.reader.Read(p)
	svf.position += int64(n)
	return n, err
}

// ReadAt implements afero.File.ReadAt
func (svf *SevenZipVirtualFile) ReadAt(p []byte, off int64) (n int, err error) {
	// This is not efficient, but it's the only way to support ReadAt with this library
	if _, err := svf.Seek(off, io.SeekStart); err != nil {
		return 0, err
	}
	return svf.Read(p)
}

// Seek implements afero.File.Seek
func (svf *SevenZipVirtualFile) Seek(offset int64, whence int) (int64, error) {
	svf.mu.Lock()
	defer svf.mu.Unlock()

	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = svf.position + offset
	case io.SeekEnd:
		abs = svf.fileMeta.FileSize + offset
	default:
		return 0, os.ErrInvalid
	}

	if abs < 0 {
		return 0, os.ErrInvalid
	}

	if abs != svf.position {
		// We need to re-open the reader to seek
		if svf.reader != nil {
			svf.reader.Close()
			svf.reader = nil
			svf.readerInitialized = false
		}
		svf.position = abs
	}

	return abs, nil
}

// Close implements afero.File.Close
func (svf *SevenZipVirtualFile) Close() error {
	svf.mu.Lock()
	defer svf.mu.Unlock()

	if svf.reader != nil {
		err := svf.reader.Close()
		svf.reader = nil
		svf.readerInitialized = false
		return err
	}
	return nil
}

// Name implements afero.File.Name
func (svf *SevenZipVirtualFile) Name() string {
	return svf.name
}

// Readdir implements afero.File.Readdir
func (svf *SevenZipVirtualFile) Readdir(count int) ([]fs.FileInfo, error) {
	return nil, ErrNotDirectory
}

// Readdirnames implements afero.File.Readdirnames
func (svf *SevenZipVirtualFile) Readdirnames(n int) ([]string, error) {
	return nil, ErrNotDirectory
}

// Stat implements afero.File.Stat
func (svf *SevenZipVirtualFile) Stat() (fs.FileInfo, error) {
	info := &MetadataFileInfo{
		name:    filepath.Base(svf.name),
		size:    svf.fileMeta.FileSize,
		mode:    0644,
		modTime: time.Unix(svf.fileMeta.ModifiedAt, 0),
		isDir:   false,
	}

	return info, nil
}

// Write implements afero.File.Write (not supported)
func (svf *SevenZipVirtualFile) Write(p []byte) (n int, err error) {
	return 0, os.ErrPermission
}

// WriteAt implements afero.File.WriteAt (not supported)
func (svf *SevenZipVirtualFile) WriteAt(p []byte, off int64) (n int, err error) {
	return 0, os.ErrPermission
}

// WriteString implements afero.File.WriteString (not supported)
func (svf *SevenZipVirtualFile) WriteString(s string) (ret int, err error) {
	return 0, os.ErrPermission
}

// Sync implements afero.File.Sync (no-op for read-only)
func (svf *SevenZipVirtualFile) Sync() error {
	return nil
}

// Truncate implements afero.File.Truncate (not supported)
func (svf *SevenZipVirtualFile) Truncate(size int64) error {
	return os.ErrPermission
}

func (svf *SevenZipVirtualFile) ensureReader() error {
	if svf.readerInitialized {
		return nil
	}

	if svf.poolManager == nil {
		return ErrNoUsenetPool
	}

	// This is inefficient, but it's the only way to do it without changing the architecture significantly
	// 1. Open the NZB file
	nzbFile, err := os.Open(svf.fileMeta.SourceNzbPath)
	if err != nil {
		return err
	}
	defer nzbFile.Close()

	// 2. Parse it
	// We need a parser here. We can't use the one from the importer, so we create a new one.
	// This is not ideal, but it's the only way without a major refactoring.
	parser := importer.NewParser(svf.poolManager)
	parsedNzb, err := parser.ParseFile(nzbFile, svf.fileMeta.SourceNzbPath)
	if err != nil {
		return err
	}

	// 3. Create a UsenetReaderAt for the archive
	var sevenZipFiles []importer.ParsedFile
	for _, f := range parsedNzb.Files {
		if f.IsSevenZipArchive {
			sevenZipFiles = append(sevenZipFiles, f)
		}
	}

	if len(sevenZipFiles) == 0 {
		return fmt.Errorf("no 7z files found in nzb")
	}

	sort.Slice(sevenZipFiles, func(i, j int) bool {
		return sevenZipFiles[i].Filename < sevenZipFiles[j].Filename
	})

	readerAt := importer.NewUsenetReaderAt(sevenZipFiles, svf.poolManager, 64, slog.Default())

	// 4. Create a sevenzip.Reader
	szr, err := sevenzip.NewReaderWithPassword(readerAt, readerAt.TotalSize(), svf.fileMeta.Password)
	if err != nil {
		return err
	}

	// 5. Find the file
	var targetFile *sevenzip.File
	for _, f := range szr.File {
		if f.Name == svf.fileMeta.InternalPath {
			targetFile = f
			break
		}
	}

	if targetFile == nil {
		return fmt.Errorf("file not found in archive: %s", svf.fileMeta.InternalPath)
	}

	// 6. Open the file
	rc, err := targetFile.Open()
	if err != nil {
		return err
	}

	// 7. Seek to the correct position
	if svf.position > 0 {
		if _, err := io.CopyN(io.Discard, rc, svf.position); err != nil {
			rc.Close()
			return err
		}
	}

	svf.reader = rc
	svf.readerInitialized = true

	return nil
}
