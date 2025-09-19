package sevenzip

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// FileEntry represents a single file within a 7z archive.
type FileEntry struct {
	Name     string
	Size     uint64
	Offset   uint64 // Absolute offset of the file data within the archive
	Modified time.Time
}

// ArchiveInfo holds information about a streamable 7z archive.
type ArchiveInfo struct {
	Files []FileEntry
}

// IsStreamable checks if a 7z archive is streamable (uncompressed).
// If it is, it returns information about the files within the archive.
func IsStreamable(r io.ReaderAt, size int64) (*ArchiveInfo, error) {
	return parse(r, size)
}

// Extract returns an io.ReadCloser for the data of the given FileEntry.
func Extract(r io.ReaderAt, fe FileEntry) (io.ReadCloser, error) {
	sr, err := extract(r, fe)
	if err != nil {
		return nil, err
	}
	return &sectionReaderCloser{SectionReader: *sr}, nil
}

// sectionReaderCloser wraps io.SectionReader to provide a Close method.
type sectionReaderCloser struct {
	io.SectionReader
}

func (s *sectionReaderCloser) Close() error {
	return nil // No-op closer
}

// StreamFileByExtension finds a file with the given extension and returns a reader for it.
func StreamFileByExtension(r io.ReaderAt, size int64, ext string) (io.ReadCloser, string, error) {
	info, err := IsStreamable(r, size)
	if err != nil {
		return nil, "", fmt.Errorf("archive is not streamable: %w", err)
	}

	var targetFile *FileEntry
	for i := range info.Files {
		if strings.HasSuffix(strings.ToLower(info.Files[i].Name), ext) {
			targetFile = &info.Files[i]
			break
		}
	}

	if targetFile == nil {
		return nil, "", fmt.Errorf("no file with extension '%s' found in archive", ext)
	}

	reader, err := Extract(r, *targetFile)
	if err != nil {
		return nil, "", err
	}

	return reader, targetFile.Name, nil
}

// StreamMKV is a convenience function to stream the first MKV file found.
func StreamMKV(r io.ReaderAt, size int64) (io.ReadCloser, string, error) {
	return StreamFileByExtension(r, size, ".mkv")
}
