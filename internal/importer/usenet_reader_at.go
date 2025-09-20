package importer

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"

	metapb "github.com/javi11/altmount/internal/metadata/proto"
	"github.com/javi11/altmount/internal/pool"
	"github.com/javi11/nntppool"
)

const (
	// Default cache size for the UsenetReaderAt cache
	defaultMaxCacheSize = 64 * 1024 * 1024 // 64 MB
)

// nntpDownloader is an interface that abstracts the NNTP connection pool.
// This is used to make UsenetReaderAt more testable.
type nntpDownloader interface {
	BodyReader(ctx context.Context, msgId string, groups []string) (io.ReadCloser, error)
}

// nntpPoolWrapper is an adapter that makes nntppool.UsenetConnectionPool implement nntpDownloader.
type nntpPoolWrapper struct {
	pool nntppool.UsenetConnectionPool
}

func (w *nntpPoolWrapper) BodyReader(ctx context.Context, msgId string, groups []string) (io.ReadCloser, error) {
	// The real BodyReader returns (nntpcli.UsenetReader, error).
	// nntpcli.UsenetReader implements io.ReadCloser.
	// We can return it directly.
	return w.pool.BodyReader(ctx, msgId, groups)
}

// UsenetReaderAt is an io.ReaderAt that reads from a Usenet connection pool.
type UsenetReaderAt struct {
	files        []ParsedFile
	TotalSize    int64
	downloader   nntpDownloader
	segmentCache sync.Map // Cache segment ID -> []byte
	cacheSize    int64
	maxCacheSize int64
	log          *slog.Logger
	mu           sync.Mutex
}

// NewUsenetReaderAt creates a new UsenetReaderAt.
func NewUsenetReaderAt(files []ParsedFile, poolManager pool.Manager, maxCacheSizeMB int, log *slog.Logger) (*UsenetReaderAt, error) {
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

	pool, err := poolManager.GetPool()
	if err != nil {
		return nil, fmt.Errorf("failed to get NNTP pool: %w", err)
	}

	return &UsenetReaderAt{
		files:        files,
		TotalSize:    totalSize,
		downloader:   &nntpPoolWrapper{pool: pool},
		log:          log,
		maxCacheSize: maxCacheBytes,
	}, nil
}

// ReadAt reads len(p) bytes into p starting at offset off in the virtual file.
func (r *UsenetReaderAt) ReadAt(p []byte, off int64) (n int, err error) {
	if off < 0 {
		return 0, fmt.Errorf("negative offset")
	}
	if off >= r.TotalSize {
		return 0, io.EOF
	}

	bytesToRead := len(p)
	if off+int64(bytesToRead) > r.TotalSize {
		bytesToRead = int(r.TotalSize - off)
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

	reader, err := r.downloader.BodyReader(context.Background(), segment.Id, file.Groups)
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
