package sevenzip

import (
	"io"
	"os"
)

// FileEntry represents a file within the 7z archive.
type FileEntry struct {
	Name   string
	Size   uint64
	Offset int64
}

// ArchiveInfo holds information about a 7z archive.
type ArchiveInfo struct {
	Files []FileEntry
}

// IsStreamable checks if a 7z archive is streamable.
// It returns ArchiveInfo if the archive is streamable, otherwise it returns an error.
func IsStreamable(path string) (*ArchiveInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	startHeader, err := ParseStartHeader(f)
	if err != nil {
		return nil, err
	}

	if _, err := f.Seek(int64(startHeader.NextHeaderOffset), io.SeekCurrent); err != nil {
		return nil, err
	}

	headerReader := io.LimitReader(f, int64(startHeader.NextHeaderSize))
	info, packPos, err := parseHeader(headerReader)
	if err != nil {
		return nil, err
	}

	baseOffset := 32 + int64(startHeader.NextHeaderOffset) + packPos
	var cumulativeSize uint64
	for i := range info.Files {
		info.Files[i].Offset = baseOffset + int64(cumulativeSize)
		cumulativeSize += info.Files[i].Size
	}

	return info, nil
}

// ExtractFileAtOffset returns an io.ReadCloser for a file using the absolute offset inside the 7z archive.
func ExtractFileAtOffset(path string, fe FileEntry) (io.ReadCloser, error) {
	return extractFileAtOffset(path, fe)
}
