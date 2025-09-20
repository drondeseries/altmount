package importer

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"

	"github.com/javi11/altmount/internal/pool"
)

// JoinStreamedArchiveParts finds all parts of a multi-part archive,
// sorts them numerically, fetches their content from usenet, and concatenates
// them into a single output file.
func JoinStreamedArchiveParts(
	ctx context.Context,
	files []ParsedFile,
	outPath string,
	poolManager pool.Manager,
	log *slog.Logger,
) error {
	// Sort files numerically based on filename
	sort.Slice(files, func(i, j int) bool {
		return files[i].Filename < files[j].Filename
	})

	log.Debug("Joining streamed archive parts", "count", len(files), "output_path", outPath)

	out, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("failed to create output file %s: %w", outPath, err)
	}
	defer out.Close()

	for i, part := range files {
		log.Debug("Processing part", "index", i+1, "filename", part.Filename, "size", part.Size)
		// Create a reader for just this part.
		// The UsenetReaderAt will fetch the segments from the pool.
		readerAt, err := NewUsenetReaderAt([]ParsedFile{part}, poolManager, 64, log)
		if err != nil {
			return fmt.Errorf("failed to create reader for part %s: %w", part.Filename, err)
		}

		// We need to copy the entire content of this part.
		// A SectionReader is a good way to do this from a ReaderAt.
		sectionReader := io.NewSectionReader(readerAt, 0, readerAt.TotalSize)

		bytesCopied, err := io.Copy(out, sectionReader)
		if err != nil {
			return fmt.Errorf("failed to copy part file %s: %w", part.Filename, err)
		}
		log.Debug("Copied part to output file", "filename", part.Filename, "bytes_copied", bytesCopied)
	}

	log.Info("Successfully joined all archive parts", "output_path", outPath)
	return nil
}
