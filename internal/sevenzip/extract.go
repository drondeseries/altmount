package sevenzip

import (
	"io"
)

// extract is the internal implementation for Extract.
func extract(r io.ReaderAt, fe FileEntry) (*io.SectionReader, error) {
	return io.NewSectionReader(r, int64(fe.Offset), int64(fe.Size)), nil
}
