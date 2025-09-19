package sevenzip

import (
	"io"
)

// extract is the internal implementation for Extract.
func extract(r io.ReaderAt, fe FileEntry) (io.ReadCloser, error) {
	// Create a section reader for the specific file entry.
	// This reader will read directly from the specified offset and limit the read to the file's size.
	reader := io.NewSectionReader(r, int64(fe.Offset), int64(fe.Size))

	// If the underlying ReaderAt is also a Closer, we should close it when we're done.
	// We create a struct that combines the reader and the closer.
	if closer, ok := r.(io.Closer); ok {
		return &sectionReadCloser{
			Reader: reader,
			Closer: closer,
		}, nil
	}

	// If the underlying reader is not a closer, we just return a no-op closer.
	return &sectionReadCloser{
		Reader: reader,
		Closer: io.NopCloser(nil),
	}, nil
}

// sectionReadCloser combines an io.Reader and an io.Closer.
// The io.SectionReader does not have a Close method, so we need to wrap it
// to ensure the underlying resource (if it's a closer) can be closed.
type sectionReadCloser struct {
	*io.SectionReader
	io.Closer
}
