package sevenzip

import (
	"bytes"
	"testing"
	"time"
)

func TestParseHeader_Uncompressed(t *testing.T) {
	// --- Manually craft the header for a streamable archive ---
	// Archive contains one file: "test.mkv", 12345 bytes, uncompressed.
	headerData := new(bytes.Buffer)

	// -- MainStreamsInfo --
	// This block describes the packed data streams.
	streamsInfo := new(bytes.Buffer)
	// kPackInfo
	streamsInfo.WriteByte(kPackInfo)
	streamsInfo.Write(encodeNumber(0)) // PackPos (offset from end of signature header)
	streamsInfo.Write(encodeNumber(1)) // NumPackStreams
	streamsInfo.WriteByte(kSize)
	streamsInfo.Write(encodeNumber(12345)) // Size of the single stream
	streamsInfo.WriteByte(kEnd)
	// kUnpackInfo
	streamsInfo.WriteByte(kUnpackInfo)
	streamsInfo.WriteByte(kFolder)
	streamsInfo.Write(encodeNumber(1)) // NumFolders
	streamsInfo.WriteByte(0)           // Not external
	// Coder info: 1 coder, ID size 1, ID=0 (Copy)
	streamsInfo.Write(encodeNumber(1))
	streamsInfo.WriteByte(0x01)
	streamsInfo.WriteByte(0x00)
	streamsInfo.WriteByte(kCodersUnpackSize)
	streamsInfo.Write(encodeNumber(12345))
	streamsInfo.WriteByte(kEnd)
	// kSubStreamsInfo (provides the sizes for files)
	streamsInfo.WriteByte(kSubStreamsInfo)
	streamsInfo.WriteByte(kSize)
	streamsInfo.Write(encodeNumber(uint64(len(encodeNumber(12345))))) // Size of the sizes property
	streamsInfo.Write(encodeNumber(12345))
	streamsInfo.WriteByte(kEnd)
	streamsInfo.WriteByte(kEnd)

	// -- FilesInfo --
	// This block describes the file metadata.
	filesInfo := new(bytes.Buffer)
	filesInfo.Write(encodeNumber(1)) // NumFiles
	// kName property
	filesInfo.WriteByte(kName)
	nameBytes := []byte{'t', 0, 'e', 0, 's', 0, 't', 0, '.', 0, 'm', 0, 'k', 0, 'v', 0, 0, 0}
	filesInfo.Write(encodeNumber(uint64(1 + len(nameBytes))))
	filesInfo.WriteByte(0) // Not external
	filesInfo.Write(nameBytes)
	// kMTime property
	filesInfo.WriteByte(kMTime)
	mtimeProp := new(bytes.Buffer)
	mtimeProp.Write(encodeBoolList([]bool{true}))
	mtimeProp.WriteByte(0) // Not external
	winTicks := uint64(133170096000000000) // 2023-01-01 12:00:00 UTC
	mtimeProp.Write(encodeNumber(winTicks))
	filesInfo.Write(encodeNumber(uint64(mtimeProp.Len())))
	filesInfo.Write(mtimeProp.Bytes())
	filesInfo.WriteByte(kEnd)

	// -- Assemble the final header --
	headerData.WriteByte(kHeader)
	headerData.WriteByte(kMainStreamsInfo)
	headerData.Write(streamsInfo.Bytes())
	headerData.WriteByte(kFilesInfo)
	headerData.Write(filesInfo.Bytes())
	headerData.WriteByte(kEnd)

	// --- Run the parser ---
	info, err := parseHeader(headerData.Bytes())
	if err != nil {
		t.Fatalf("parseHeader failed: %v", err)
	}

	// --- Validate the results ---
	if info == nil {
		t.Fatal("parseHeader returned nil info")
	}
	if len(info.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(info.Files))
	}

	file := info.Files[0]
	if file.Name != "test.mkv" {
		t.Errorf("expected file name 'test.mkv', got '%s'", file.Name)
	}
	if file.Size != 12345 {
		t.Errorf("expected file size 12345, got %d", file.Size)
	}

	expectedTime := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)
	if !file.Modified.Equal(expectedTime) {
		t.Errorf("expected mod time %v, got %v", expectedTime, file.Modified)
	}

	// Offset should be 32 (signature) + 0 (packpos)
	if file.Offset != 32 {
		t.Errorf("expected file offset 32, got %d", file.Offset)
	}
}

func TestParseHeader_Compressed(t *testing.T) {
	// --- Manually craft the header for a compressed archive ---
	headerData := new(bytes.Buffer)

	// -- MainStreamsInfo --
	streamsInfo := new(bytes.Buffer)
	// kPackInfo
	streamsInfo.WriteByte(kPackInfo)
	streamsInfo.Write(encodeNumber(0))
	streamsInfo.Write(encodeNumber(1))
	streamsInfo.WriteByte(kSize)
	streamsInfo.Write(encodeNumber(100))
	streamsInfo.WriteByte(kEnd)
	// kUnpackInfo
	streamsInfo.WriteByte(kUnpackInfo)
	streamsInfo.WriteByte(kFolder)
	streamsInfo.Write(encodeNumber(1)) // NumFolders
	streamsInfo.WriteByte(0)           // Not external
	// Coder info: 1 coder, ID size 3, ID=0x030101 (LZMA)
	streamsInfo.Write(encodeNumber(1))
	streamsInfo.WriteByte(0x03)
	streamsInfo.Write([]byte{0x03, 0x01, 0x01})
	streamsInfo.WriteByte(kEnd)
	// ... the rest of the header doesn't matter as it should fail on the codec

	// -- Assemble the final header --
	headerData.WriteByte(kHeader)
	headerData.WriteByte(kMainStreamsInfo)
	headerData.Write(streamsInfo.Bytes())
	headerData.WriteByte(kEnd)

	// --- Run the parser ---
	_, err := parseHeader(headerData.Bytes())

	// --- Validate the results ---
	if err == nil {
		t.Fatal("expected an error for compressed archive, but got nil")
	}
	if err != errUnsupportedCodec {
		t.Errorf("expected error %v, got %v", errUnsupportedCodec, err)
	}
}

// Corrected helper to encode a number in 7z's variable-length format.
func encodeNumber(n uint64) []byte {
	if n < 0x80 {
		return []byte{byte(n)}
	}
	if n < 0x8000 {
		return []byte{0x80 | byte(n>>8), byte(n)}
	}
	if n < 0x800000 {
		return []byte{0xC0 | byte(n>>16), byte(n >> 8), byte(n)}
	}
	if n < 0x80000000 {
		return []byte{0xE0 | byte(n>>24), byte(n >> 16), byte(n >> 8), byte(n)}
	}

	// For larger numbers, it's more complex, but this covers most cases for tests.
	buf := make([]byte, 9)
	buf[0] = 0xFF
	for i := 1; i <= 8; i++ {
		buf[i] = byte(n >> (8 * (i - 1)))
	}
	return buf
}

// Helper to encode a boolean list.
func encodeBoolList(b []bool) []byte {
	if len(b) == 0 {
		return []byte{0}
	}
	allTrue := true
	for _, v := range b {
		if !v {
			allTrue = false
			break
		}
	}
	if allTrue {
		return []byte{1}
	}

	var buf bytes.Buffer
	buf.WriteByte(0) // Not all are true

	var currentByte byte
	mask := byte(0x80)
	for _, v := range b {
		if v {
			currentByte |= mask
		}
		mask >>= 1
		if mask == 0 {
			buf.WriteByte(currentByte)
			currentByte = 0
			mask = 0x80
		}
	}
	if mask != 0x80 {
		buf.WriteByte(currentByte)
	}
	return buf.Bytes()
}
