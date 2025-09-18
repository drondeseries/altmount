package importer

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"time"

	"github.com/bodgit/sevenzip"
	metapb "github.com/javi11/altmount/internal/metadata/proto"
	"github.com/javi11/altmount/internal/pool"
)

// SevenZipProcessor interface for analyzing 7z content from NZB data
type SevenZipProcessor interface {
	Analyze7zContentFromNzb(ctx context.Context, sevenZipFiles []ParsedFile) ([]sevenZipContent, error)
	CreateFileMetadataFrom7zContent(sevenZipContent sevenZipContent, sourceNzbPath string) *metapb.FileMetadata
}

// sevenZipContent represents a file within a 7z archive for processing
type sevenZipContent struct {
	InternalPath string    `json:"internal_path"`
	Filename     string    `json:"filename"`
	Size         int64     `json:"size"`
	ModTime      time.Time `json:"mod_time"`
	CreateTime   time.Time `json:"create_time"`
	IsDirectory  bool      `json:"is_directory,omitempty"`
}

// sevenZipProcessor handles 7z archive analysis and content extraction
type sevenZipProcessor struct {
	log         *slog.Logger
	poolManager pool.Manager
}

// NewSevenZipProcessor creates a new 7z processor
func NewSevenZipProcessor(poolManager pool.Manager) SevenZipProcessor {
	return &sevenZipProcessor{
		log:         slog.Default().With("component", "7z-processor"),
		poolManager: poolManager,
	}
}

// CreateFileMetadataFrom7zContent creates FileMetadata from sevenZipContent for the metadata system
func (p *sevenZipProcessor) CreateFileMetadataFrom7zContent(
	content sevenZipContent,
	sourceNzbPath string,
) *metapb.FileMetadata {
	modTime := content.ModTime.Unix()
	createTime := content.CreateTime.Unix()
	if createTime == 0 {
		createTime = modTime
	}

	return &metapb.FileMetadata{
		FileSize:      content.Size,
		SourceNzbPath: sourceNzbPath,
		Status:        metapb.FileStatus_FILE_STATUS_HEALTHY,
		CreatedAt:     createTime,
		ModifiedAt:    modTime,
		SegmentData:   nil, // Segments are handled by the UsenetReaderAt
		InternalPath:  content.InternalPath,
	}
}

// Analyze7zContentFromNzb analyzes a 7z archive using the UsenetReaderAt adapter
func (p *sevenZipProcessor) Analyze7zContentFromNzb(ctx context.Context, sevenZipFiles []ParsedFile) ([]sevenZipContent, error) {
	if p.poolManager == nil {
		return nil, NewNonRetryableError("no pool manager available", nil)
	}
	if len(sevenZipFiles) == 0 {
		return nil, NewNonRetryableError("no 7z files provided", nil)
	}

	// Sort files to handle multi-volume archives correctly
	sort.Slice(sevenZipFiles, func(i, j int) bool {
		return sevenZipFiles[i].Filename < sevenZipFiles[j].Filename
	})

	// Use the UsenetReaderAt adapter
	readerAt := NewUsenetReaderAt(sevenZipFiles, p.poolManager, 64, p.log)

	// Create a new 7z reader
	var (
		szr *sevenzip.Reader
		err error
	)
	if len(sevenZipFiles) > 0 && sevenZipFiles[0].Password != "" {
		szr, err = sevenzip.NewReaderWithPassword(readerAt, readerAt.totalSize, sevenZipFiles[0].Password)
	} else {
		szr, err = sevenzip.NewReader(readerAt, readerAt.totalSize)
	}
	if err != nil {
		return nil, NewNonRetryableError(fmt.Sprintf("failed to create 7z reader: %v", err), err)
	}

	// Extract file information from the archive
	var contents []sevenZipContent
	for _, file := range szr.File {
		contents = append(contents, sevenZipContent{
			InternalPath: file.Name,
			Filename:     filepath.Base(file.Name),
			Size:         int64(file.UncompressedSize),
			ModTime:      file.Modified,
			CreateTime:   file.Created,
			IsDirectory:  file.FileInfo().IsDir(),
		})
	}

	p.log.Info("Successfully analyzed 7z archive", "files_found", len(contents))

	return contents, nil
}
