package sevenzip

import (
	"bytes"
	"errors"
	"testing"
)

func TestParseHeader_Uncompressed(t *testing.T) {
	// --- Manually craft the header for a streamable archive ---
	headerData := new(bytes.Buffer)

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
	streamsInfo.Write(encodeNumber(1))
	streamsInfo.WriteByte(0)
	streamsInfo.Write(encodeNumber(1))
	streamsInfo.WriteByte(0x01)
	streamsInfo.WriteByte(0x00)
	streamsInfo.WriteByte(kCodersUnpackSize)
	streamsInfo.Write(encodeNumber(100))
	streamsInfo.WriteByte(kEnd)
	streamsInfo.WriteByte(kSubStreamsInfo)
	streamsInfo.WriteByte(kNumUnpackStream)
	streamsInfo.Write(encodeNumber(1))
	streamsInfo.WriteByte(kSize)
	streamsInfo.Write(encodeNumber(100))
	streamsInfo.WriteByte(kEnd)
	streamsInfo.WriteByte(kEnd)

	// -- FilesInfo --
	filesInfo := new(bytes.Buffer)
	filesInfo.Write(encodeNumber(1))
	filesInfo.WriteByte(kName)
	nameBytes := []byte{'t', 0, 'e', 0, 's', 0, 't', 0, '.', 0, 'm', 0, 'k', 0, 'v', 0, 0, 0}
	filesInfo.Write(encodeNumber(uint64(1 + len(nameBytes))))
	filesInfo.WriteByte(0)
	filesInfo.Write(nameBytes)
	filesInfo.WriteByte(kEnd)

	// -- Assemble final header --
	headerData.WriteByte(kHeader)
	headerData.WriteByte(kMainStreamsInfo)
	headerData.Write(streamsInfo.Bytes())
	headerData.WriteByte(kFilesInfo)
	headerData.Write(filesInfo.Bytes())
	headerData.WriteByte(kEnd)

	// --- Run the parser ---
	// In a real scenario, baseOffset would be the signature size (32).
	// The packPos inside the header is relative to the start of the header data,
	// but the final file offset is calculated from the start of the file.
	// The test data has packPos=0, so the file data starts where the header ends.
	// But the final offset calculation in parseDecodedHeader adds baseOffset.
	// Let's pass 0 and adjust the expectation.
	info, err := parseHeader(nil, headerData.Bytes(), 0)
	if err != nil {
		t.Fatalf("parseHeader failed: %v", err)
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
	if file.Offset != 0 {
		t.Errorf("expected offset 0, got %d", file.Offset)
	}
}

func TestParseHeader_Compressed(t *testing.T) {
	// This test crafts a header for an archive that uses LZMA compression
	// for its file data, which our streamable parser should reject.
	headerData := new(bytes.Buffer)

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
	streamsInfo.WriteByte(0) // external = false
	streamsInfo.Write(encodeNumber(1)) // numCoders
	// Coder attributes: codec ID size = 3, no properties
	streamsInfo.WriteByte(byte(len([]byte{0x03, 0x01, 0x01})))
	streamsInfo.Write([]byte{0x03, 0x01, 0x01}) // LZMA codec ID
	streamsInfo.WriteByte(kCodersUnpackSize)
	streamsInfo.Write(encodeNumber(100))
	streamsInfo.WriteByte(kEnd)
	streamsInfo.WriteByte(kSubStreamsInfo)
	streamsInfo.WriteByte(kNumUnpackStream)
	streamsInfo.Write(encodeNumber(1))
	streamsInfo.WriteByte(kSize)
	streamsInfo.Write(encodeNumber(100))
	streamsInfo.WriteByte(kEnd)
	streamsInfo.WriteByte(kEnd)

	// -- FilesInfo --
	filesInfo := new(bytes.Buffer)
	filesInfo.Write(encodeNumber(1))
	filesInfo.WriteByte(kName)
	nameBytes := []byte{'t', 0, 'e', 0, 's', 0, 't', 0, '.', 0, 'm', 0, 'k', 0, 'v', 0, 0, 0}
	filesInfo.Write(encodeNumber(uint64(1 + len(nameBytes))))
	filesInfo.WriteByte(0)
	filesInfo.Write(nameBytes)
	filesInfo.WriteByte(kEnd)

	// -- Assemble final header --
	headerData.WriteByte(kHeader)
	headerData.WriteByte(kMainStreamsInfo)
	headerData.Write(streamsInfo.Bytes())
	headerData.WriteByte(kFilesInfo)
	headerData.Write(filesInfo.Bytes())
	headerData.WriteByte(kEnd)

	_, err := parseHeader(nil, headerData.Bytes(), 0)
	if !errors.Is(err, errUnsupportedCodec) {
		t.Errorf("expected error %v, got %v", errUnsupportedCodec, err)
	}
}

func encodeNumber(n uint64) []byte {
	buf := new(bytes.Buffer)
	if n < 0x80 {
		buf.WriteByte(byte(n))
		return buf.Bytes()
	}

	var highBits byte = 0x80
	temp := n
	for i := 0; i < 8; i++ {
		if temp < (1 << (7 + i*8)) {
			highBits |= byte(n >> (8 * (i + 1)))
			buf.WriteByte(highBits)
			for j := i; j >= 0; j-- {
				buf.WriteByte(byte(n >> (8 * j)))
			}
			return buf.Bytes()
		}
	}
	return nil
}

func encodeBoolList(b []bool) []byte {
	buf := new(bytes.Buffer)
	allTrue := true
	for _, v := range b {
		if !v {
			allTrue = false
			break
		}
	}
	if allTrue {
		buf.WriteByte(1)
		return buf.Bytes()
	}

	buf.WriteByte(0)
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
