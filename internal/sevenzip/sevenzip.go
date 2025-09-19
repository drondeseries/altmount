package sevenzip

import (
	"io"
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
// It takes an io.ReaderAt to allow reading from different parts of the archive without reading it sequentially.
func IsStreamable(r io.ReaderAt, size int64) (*ArchiveInfo, error) {
	// Implementation will be in parse.go
	return parse(r, size)
}

// Extract opens the archive from the given io.ReaderAt and returns an io.ReadCloser
// for the data of the given FileEntry. The reader will be a section reader,
// which is efficient for direct streaming.
func Extract(r io.ReaderAt, fe FileEntry) (io.ReadCloser, error) {
	// Implementation will be in extract.go
	return extract(r, fe)
}
