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
func IsStreamable(r io.ReaderAt, size int64) (*ArchiveInfo, error) {
	return parse(r, size)
}

// Extract returns an *io.SectionReader for the data of the given FileEntry.
// It is the caller's responsibility to manage the lifecycle of the underlying ReaderAt.
func Extract(r io.ReaderAt, fe FileEntry) (*io.SectionReader, error) {
	return extract(r, fe)
}
