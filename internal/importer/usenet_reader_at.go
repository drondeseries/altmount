package importer

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"

	"github.com/javi11/altmount/internal/pool"
	metapb "github.com/javi11/altmount/internal/metadata/proto"
)

const (
	// Default cache size for the UsenetReaderAt cache
	defaultMaxCacheSize = 64 * 1024 * 1024 // 64 MB
)

// UsenetReaderAt is an io.ReaderAt that reads from a Usenet connection pool.
// It is designed to provide a seekable interface over a non-seekable stream of NZB segments.
type UsenetReaderAt struct {
	files        []ParsedFile
	totalSize    int64
	poolManager  pool.Manager
	segmentCache sync.Map // Cache segment ID -> []byte
	cacheSize    int64
	maxCacheSize int64
	log          *slog.Logger
	mu           sync.Mutex
}

// NewUsenetReaderAt creates a new UsenetReaderAt.
func NewUsenetReaderAt(files []ParsedFile, poolManager pool.Manager, maxCacheSizeMB int, log *slog.Logger) *UsenetReaderAt {
	var totalSize int64
	for _, file := range files {
		for _, segment := range file.Segments {
			totalSize += segment.SegmentSize
		}
	}

	maxCacheBytes := int64(maxCacheSizeMB) * 1024 * 1024
	if maxCacheBytes <= 0 {
		maxCacheBytes = defaultMaxCacheSize
	}

	return &UsenetReaderAt{
		files:        files,
		totalSize:    totalSize,
		poolManager:  poolManager,
		log:          log,
		maxCacheSize: maxCacheBytes,
	}
}

// ReadAt reads len(p) bytes into p starting at offset off in the virtual file.
func (r *UsenetReaderAt) ReadAt(p []byte, off int64) (n int, err error) {
	if off < 0 {
		return 0, fmt.Errorf("negative offset")
	}
	if off >= r.totalSize {
		return 0, io.EOF
	}

	bytesToRead := len(p)
	if off+int64(bytesToRead) > r.totalSize {
		bytesToRead = int(r.totalSize - off)
		// We will read until the end of the file, so the error should be EOF.
		err = io.EOF
	}

	var currentFileOffset int64
	var totalBytesRead int

	for _, file := range r.files {
		for _, segment := range file.Segments {
			segmentSize := segment.SegmentSize
			if off >= currentFileOffset+segmentSize {
				currentFileOffset += segmentSize
				continue
			}

			// We need to read from this segment.
			segmentReadOffset := off - currentFileOffset

			segmentData, fetchErr := r.getSegmentData(file, segment)
			if fetchErr != nil {
				return totalBytesRead, fetchErr
			}

			bytesToCopy := int(segmentSize - segmentReadOffset)
			if bytesToCopy > bytesToRead-totalBytesRead {
				bytesToCopy = bytesToRead - totalBytesRead
			}

			copied := copy(p[totalBytesRead:], segmentData[segmentReadOffset:segmentReadOffset+int64(bytesToCopy)])
			totalBytesRead += copied
			off += int64(copied)

			if totalBytesRead == bytesToRead {
				return totalBytesRead, err // err might be io.EOF here
			}

			currentFileOffset += segmentSize
		}
	}

	// Should not be reached if logic is correct, but as a safeguard:
    if totalBytesRead > 0 {
        return totalBytesRead, io.EOF
    }

	return totalBytesRead, io.EOF
}

func (r *UsenetReaderAt) getSegmentData(file ParsedFile, segment *metapb.SegmentData) ([]byte, error) {
	if data, ok := r.segmentCache.Load(segment.Id); ok {
		return data.([]byte), nil
	}

	cp, err := r.poolManager.GetPool()
	if err != nil {
		return nil, err
	}

	reader, err := cp.BodyReader(context.Background(), segment.Id, file.Groups)
	if err != nil {
		return nil, fmt.Errorf("failed to get body reader for segment %s: %w", segment.Id, err)
	}
	defer reader.Close()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, reader); err != nil {
		return nil, err
	}

	segmentData := buf.Bytes()

	r.mu.Lock()
	if r.cacheSize+int64(len(segmentData)) <= r.maxCacheSize {
		r.segmentCache.Store(segment.Id, segmentData)
		r.cacheSize += int64(len(segmentData))
	}
	r.mu.Unlock()

	return segmentData, nil
}
