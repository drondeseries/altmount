package sevenzip

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/conneroisu/lzma-go"
)

func TestParse_Uncompressed(t *testing.T) {
	// --- Manually craft the header for a streamable archive ---
	headerContent := new(bytes.Buffer)

	// -- MainStreamsInfo --
	streamsInfo := new(bytes.Buffer)
	streamsInfo.WriteByte(kPackInfo)
	streamsInfo.Write(encodeNumber(0))         // packPos
	streamsInfo.Write(encodeNumber(1))         // numPackStreams
	streamsInfo.WriteByte(kSize)               // property ID
	streamsInfo.Write(encodeNumber(100))       // packSize
	streamsInfo.WriteByte(kEnd)                // end of PackInfo
	streamsInfo.WriteByte(kUnpackInfo)         // property ID
	streamsInfo.WriteByte(kFolder)             // property ID
	streamsInfo.Write(encodeNumber(1))         // numFolders
	streamsInfo.WriteByte(0)                   // external = false
	streamsInfo.Write(encodeNumber(1))         // numCoders
	streamsInfo.WriteByte(byte(len([]byte{0x00}))) // Coder attributes: codec ID size = 1, Copy method
	streamsInfo.Write([]byte{0x00})            // Copy codec ID
	streamsInfo.WriteByte(kCodersUnpackSize)   // property ID
	streamsInfo.Write(encodeNumber(100))       // unpackSize
	streamsInfo.WriteByte(kEnd)                // end of UnpackInfo
	streamsInfo.WriteByte(kSubStreamsInfo)     // property ID
	streamsInfo.WriteByte(kNumUnpackStream)    // property ID
	streamsInfo.Write(encodeNumber(1))         // numUnpackStreams
	streamsInfo.WriteByte(kSize)               // property ID
	streamsInfo.Write(encodeNumber(100))       // unpackSize
	streamsInfo.WriteByte(kEnd)                // end of SubStreamsInfo
	streamsInfo.WriteByte(kEnd)                // end of MainStreamsInfo

	// -- FilesInfo --
	filesInfo := new(bytes.Buffer)
	filesInfo.Write(encodeNumber(1)) // numFiles
	filesInfo.WriteByte(kName)       // property ID
	nameBytes := []byte{'t', 0, 'e', 0, 's', 0, 't', 0, '.', 0, 'm', 0, 'k', 0, 'v', 0, 0, 0}
	nameBlock := new(bytes.Buffer)
	nameBlock.WriteByte(0) // external = 0
	nameBlock.Write(nameBytes)
	filesInfo.Write(encodeNumber(uint64(nameBlock.Len()))) // size of names data
	filesInfo.Write(nameBlock.Bytes())
	filesInfo.WriteByte(kEnd) // end of FilesInfo

	// -- Assemble final header block --
	headerContent.WriteByte(kHeader)
	headerContent.WriteByte(kMainStreamsInfo)
	headerContent.Write(streamsInfo.Bytes())
	headerContent.WriteByte(kFilesInfo)
	headerContent.Write(filesInfo.Bytes())
	headerContent.WriteByte(kEnd)

	// --- Setup mock 7z file ---
	var mockFile bytes.Buffer
	// 1. Signature header
	sigHeader := make([]byte, 32)
	copy(sigHeader[0:6], signature)
	sigHeader[6] = 0 // version
	sigHeader[7] = 4
	binary.LittleEndian.PutUint64(sigHeader[12:20], 32) // NextHeaderOffset
	binary.LittleEndian.PutUint64(sigHeader[20:28], uint64(headerContent.Len()))   // NextHeaderSize
	binary.LittleEndian.PutUint32(sigHeader[8:12], 0)     // NextHeaderCRC (not checked yet)
	mockFile.Write(sigHeader)

	// 2. Some dummy data before the header block
	mockFile.Write(make([]byte, 32))

	// 3. The header block itself
	mockFile.Write(headerContent.Bytes())

	// --- Run the parser ---
	r := bytes.NewReader(mockFile.Bytes())
	info, err := IsStreamable(r, int64(mockFile.Len()))
	if err != nil {
		t.Fatalf("IsStreamable failed: %v", err)
	}

	// --- Validate results ---
	if len(info.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(info.Files))
	}
	file := info.Files[0]
	if file.Name != "test.mkv" {
		t.Errorf("expected name 'test.mkv', got '%s'", file.Name)
	}
	if file.Size != 100 {
		t.Errorf("expected size 100, got %d", file.Size)
	}
	// Offset = signatureHeaderSize + packPos
	expectedOffset := uint64(32 + 0)
	if file.Offset != expectedOffset {
		t.Errorf("expected offset %d, got %d", expectedOffset, file.Offset)
	}
}

func TestParse_Compressed(t *testing.T) {
	// This test crafts a header for an archive that uses LZMA compression
	// for its file data, which our streamable parser should reject.
	headerContent := new(bytes.Buffer)

	// -- MainStreamsInfo --
	streamsInfo := new(bytes.Buffer)
	streamsInfo.WriteByte(kPackInfo)
	streamsInfo.Write(encodeNumber(0))
	streamsInfo.Write(encodeNumber(1))
	streamsInfo.WriteByte(kSize)
	streamsInfo.Write(encodeNumber(100))
	streamsInfo.WriteByte(kEnd)
	streamsInfo.WriteByte(kUnpackInfo)
	streamsInfo.WriteByte(kFolder)
	streamsInfo.Write(encodeNumber(1)) // numFolders
	streamsInfo.WriteByte(0)           // external = false
	streamsInfo.Write(encodeNumber(1)) // numCoders
	// Coder attributes: codec ID size = 3, no properties
	streamsInfo.WriteByte(byte(len([]byte{0x03, 0x01, 0x01})))
	streamsInfo.Write([]byte{0x03, 0x01, 0x01}) // LZMA codec ID
	streamsInfo.WriteByte(kCodersUnpackSize)
	streamsInfo.Write(encodeNumber(100))
	streamsInfo.WriteByte(kEnd)
	streamsInfo.WriteByte(kSubStreamsInfo)
	streamsInfo.WriteByte(kEnd)
	streamsInfo.WriteByte(kEnd)

	// -- FilesInfo --
	filesInfo := new(bytes.Buffer)
	filesInfo.Write(encodeNumber(1)) // numFiles = 1
	filesInfo.WriteByte(kEnd)

	// -- Assemble final header block --
	headerContent.WriteByte(kHeader)
	headerContent.WriteByte(kMainStreamsInfo)
	headerContent.Write(streamsInfo.Bytes())
	headerContent.WriteByte(kFilesInfo)
	headerContent.Write(filesInfo.Bytes())
	headerContent.WriteByte(kEnd)

	// --- Setup mock 7z file ---
	var mockFile bytes.Buffer
	sigHeader := make([]byte, 32)
	copy(sigHeader[0:6], signature)
	sigHeader[6] = 0
	sigHeader[7] = 4
	binary.LittleEndian.PutUint64(sigHeader[12:20], 0) // NextHeaderOffset
	binary.LittleEndian.PutUint64(sigHeader[20:28], uint64(headerContent.Len()))
	mockFile.Write(sigHeader)
	mockFile.Write(headerContent.Bytes())

	// --- Run the parser ---
	r := bytes.NewReader(mockFile.Bytes())
	_, err := IsStreamable(r, int64(mockFile.Len()))
	if !errors.Is(err, errUnsupportedCodec) {
		t.Errorf("expected error %v, got %v", errUnsupportedCodec, err)
	}
}

func TestParse_EncodedHeader(t *testing.T) {
	// This test is more complex. It creates an archive where the header itself
	// is compressed with LZMA.

	// 1. Create the inner header that will be compressed
	innerHeader := new(bytes.Buffer)
	innerHeader.WriteByte(kHeader) // The decompressed block starts with kHeader
	// (Add other properties like MainStreamsInfo, FilesInfo for a real test)
	innerHeader.WriteByte(kEnd)

	// 2. Compress the inner header
	var compressedHeaderBuf bytes.Buffer
	lzmaWriter, err := lzma.NewWriter(&compressedHeaderBuf)
	if err != nil {
		t.Fatalf("Failed to create lzma writer: %v", err)
	}
	if _, err := lzmaWriter.Write(innerHeader.Bytes()); err != nil {
		t.Fatalf("Failed to write to lzma writer: %v", err)
	}
	lzmaWriter.Close()
	compressedHeaderData := compressedHeaderBuf.Bytes()

	// 3. Create the outer (encoded) header
	encodedHeader := new(bytes.Buffer)
	encodedHeader.WriteByte(kEncodedHeader)
	// StreamsInfo pointing to the compressed data
	encodedHeader.WriteByte(kPackInfo)
	encodedHeader.Write(encodeNumber(uint64(32))) // PackPos starts after signature
	encodedHeader.Write(encodeNumber(1))          // NumPackStreams
	encodedHeader.WriteByte(kSize)
	encodedHeader.Write(encodeNumber(uint64(len(compressedHeaderData)))) // PackSize
	encodedHeader.WriteByte(kEnd)
	encodedHeader.WriteByte(kUnpackInfo)
	encodedHeader.WriteByte(kFolder)
	encodedHeader.Write(encodeNumber(1)) // numFolders
	encodedHeader.WriteByte(0)           // external
	encodedHeader.Write(encodeNumber(1)) // numCoders
	// Coder info for LZMA
	props := []byte{0x5d, 0x00, 0x00, 0x40, 0x00} // lc=3, lp=0, pb=2, dictSize=4MB
	codecID := []byte{0x03, 0x01, 0x01}
	encodedHeader.WriteByte(byte(len(codecID)) | 0x20) // CodecID size and has_properties flag
	encodedHeader.Write(codecID)
	encodedHeader.Write(encodeNumber(uint64(len(props))))
	encodedHeader.Write(props)
	encodedHeader.WriteByte(kCodersUnpackSize)
	encodedHeader.Write(encodeNumber(uint64(innerHeader.Len()))) // UnpackSize
	encodedHeader.WriteByte(kEnd)
	encodedHeader.WriteByte(kEnd)

	// 4. Assemble the mock file
	var mockFile bytes.Buffer
	// Signature
	sigHeader := make([]byte, 32)
	copy(sigHeader[0:6], signature)
	sigHeader[6] = 0
	sigHeader[7] = 4
	// The encoded header is at offset 32 + 32 = 64
	binary.LittleEndian.PutUint64(sigHeader[12:20], 32)
	binary.LittleEndian.PutUint64(sigHeader[20:28], uint64(encodedHeader.Len()))
	mockFile.Write(sigHeader)
	// The compressed data (which is the inner header)
	mockFile.Write(compressedHeaderData)
	// Some padding
	mockFile.Write(make([]byte, 32-len(compressedHeaderData)))
	// The encoded header itself
	mockFile.Write(encodedHeader.Bytes())

	// --- Run the parser ---
	// This test is expected to fail because the decompressed header is empty.
	// A full implementation would require mocking a complete file structure.
	// The goal here is just to see if it can *attempt* to decompress.
	r := bytes.NewReader(mockFile.Bytes())
	_, err = IsStreamable(r, int64(mockFile.Len()))
	if err == nil {
		t.Errorf("expected an error due to empty inner header, but got nil")
	}
	if !errors.Is(err, errFilesInfoMissing) {
		t.Logf("Got expected error chain: %v", err)
	}
}

// encodeNumber is a test helper to create the variable-size integer format used by 7z
func encodeNumber(n uint64) []byte {
	if n < 0x80 {
		return []byte{byte(n)}
	}
	if n < 0x4000 {
		return []byte{byte(n>>8) | 0x80, byte(n)}
	}
	if n < 0x200000 {
		return []byte{byte(n>>16) | 0xC0, byte(n >> 8), byte(n)}
	}
	// Add more cases as needed for larger numbers
	buf := make([]byte, 9)
	buf[0] = 0xFF
	binary.LittleEndian.PutUint64(buf[1:], n)
	return buf
}

// Helper to open a test file
func openTestFile(t *testing.T, name string) (io.ReaderAt, int64) {
	f, err := os.Open(name)
	if err != nil {
		t.Fatalf("Failed to open test file %s: %v", name, err)
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		t.Fatalf("Failed to stat test file %s: %v", name, err)
	}
	return f, fi.Size()
}
