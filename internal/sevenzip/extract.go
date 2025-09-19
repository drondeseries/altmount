package sevenzip

import (
	"io"
	"os"
)

// extractFileAtOffset returns an io.ReadCloser for a file using the absolute offset inside the 7z archive.
func extractFileAtOffset(path string, fe FileEntry) (io.ReadCloser, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	if _, err := f.Seek(fe.Offset, io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}

	return &fileSectionReader{
		f:      f,
		reader: io.LimitReader(f, int64(fe.Size)),
		seeker: f,
	}, nil
}

type fileSectionReader struct {
	f      *os.File
	reader io.Reader
	seeker io.Seeker
}

func (r *fileSectionReader) Read(p []byte) (n int, err error) {
	return r.reader.Read(p)
}

func (r *fileSectionReader) Close() error {
	return r.f.Close()
}

func (r *fileSectionReader) Seek(offset int64, whence int) (int64, error) {
	return r.seeker.Seek(offset, whence)
}
