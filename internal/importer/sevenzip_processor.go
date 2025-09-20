package importer

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	metapb "github.com/javi11/altmount/internal/metadata/proto"
	"github.com/javi11/altmount/internal/pool"
	"github.com/javi11/altmount/internal/sevenzip"
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

var multiPart7zPattern = regexp.MustCompile(`(?i)\.7z\.\d+$`)

// Analyze7zContentFromNzb analyzes a 7z archive using the new streamable sevenzip package.
// It now handles both single-file and multi-part 7z archives.
func (p *sevenZipProcessor) Analyze7zContentFromNzb(ctx context.Context, sevenZipFiles []ParsedFile) ([]sevenZipContent, error) {
	if p.poolManager == nil {
		return nil, NewNonRetryableError("no pool manager available", nil)
	}
	if len(sevenZipFiles) == 0 {
		return nil, NewNonRetryableError("no 7z files provided", nil)
	}

	sort.Slice(sevenZipFiles, func(i, j int) bool {
		return sevenZipFiles[i].Filename < sevenZipFiles[j].Filename
	})

	// Check if we are dealing with a multi-part archive
	isMultiPart := len(sevenZipFiles) > 1 || (len(sevenZipFiles) == 1 && multiPart7zPattern.MatchString(sevenZipFiles[0].Filename))

	var info *sevenzip.ArchiveInfo
	var err error

	if isMultiPart {
		p.log.Info("Multi-part 7z archive detected, joining parts first.", "parts", len(sevenZipFiles))
		// 1. Create a temporary file for the joined archive
		tmpFile, err := os.CreateTemp("", "altmount-joined-7z-*.7z")
		if err != nil {
			return nil, NewNonRetryableError("failed to create temporary file for joined archive", err)
		}
		defer func() {
			if err := os.Remove(tmpFile.Name()); err != nil {
				p.log.Warn("Failed to remove temporary file", "path", tmpFile.Name(), "error", err)
			}
		}()
		tmpFile.Close() // Close immediately, JoinStreamedArchiveParts will reopen it

		// 2. Call the new JoinStreamedArchiveParts
		err = JoinStreamedArchiveParts(ctx, sevenZipFiles, tmpFile.Name(), p.poolManager, p.log)
		if err != nil {
			return nil, NewNonRetryableError("failed to join multi-part 7z archive", err)
		}

		// 3. Open the joined file and pass it to sevenzip.IsStreamable
		f, err := os.Open(tmpFile.Name())
		if err != nil {
			return nil, NewNonRetryableError("failed to open joined 7z archive", err)
		}
		defer f.Close()

		fi, err := f.Stat()
		if err != nil {
			return nil, NewNonRetryableError("failed to stat joined 7z archive", err)
		}

		info, err = sevenzip.IsStreamable(f, fi.Size())

	} else {
		p.log.Info("Single-file 7z archive detected, streaming directly.")
		// Original logic for single-file archives
		readerAt, err := NewUsenetReaderAt(sevenZipFiles, p.poolManager, 64, p.log)
		if err != nil {
			return nil, NewNonRetryableError("failed to create usenet reader", err)
		}
		info, err = sevenzip.IsStreamable(readerAt, readerAt.TotalSize)
	}

	if err != nil {
		return nil, NewNonRetryableError(fmt.Sprintf("archive is not streamable or is corrupt: %v", err), err)
	}

	var contents []sevenZipContent
	for _, file := range info.Files {
		contents = append(contents, sevenZipContent{
			InternalPath: file.Name,
			Filename:     filepath.Base(file.Name),
			Size:         int64(file.Size),
			ModTime:      file.Modified,
			CreateTime:   file.Modified,
			IsDirectory:  file.Size == 0 && (len(file.Name) > 0 && file.Name[len(file.Name)-1] == filepath.Separator),
		})
	}

	p.log.Info("Successfully analyzed streamable 7z archive", "files_found", len(contents))

	return contents, nil
}
