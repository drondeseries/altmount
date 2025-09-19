package sevenzip

import (
	"bytes"
	"errors"
	"testing"
	"time"
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
	streamsInfo.Write(encodeNumber(12345))
	streamsInfo.WriteByte(kEnd)
	streamsInfo.WriteByte(kUnpackInfo)
	streamsInfo.WriteByte(kFolder)
	streamsInfo.Write(encodeNumber(1))
	streamsInfo.WriteByte(0)
	streamsInfo.Write(encodeNumber(1))
	streamsInfo.WriteByte(0x01)
	streamsInfo.WriteByte(0x00)
	streamsInfo.WriteByte(kCodersUnpackSize)
	streamsInfo.Write(encodeNumber(12345))
	streamsInfo.WriteByte(kEnd)
	streamsInfo.WriteByte(kSubStreamsInfo)
	sizeProp := new(bytes.Buffer)
	sizeProp.Write(encodeNumber(12345))
	streamsInfo.WriteByte(kSize)
	streamsInfo.Write(encodeNumber(uint64(sizeProp.Len())))
	streamsInfo.Write(sizeProp.Bytes())
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
	filesInfo.WriteByte(kMTime)
	mtimeProp := new(bytes.Buffer)
	mtimeProp.Write(encodeBoolList([]bool{true}))
	mtimeProp.WriteByte(0)
	winTicks := uint64(133170096000000000)
	mtimeProp.Write(encodeNumber(winTicks))
	filesInfo.Write(encodeNumber(uint64(mtimeProp.Len())))
	filesInfo.Write(mtimeProp.Bytes())
	filesInfo.WriteByte(kEnd)

	// -- Assemble final header --
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

	// --- Validate results ---
	if len(info.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(info.Files))
	}
	file := info.Files[0]
	if file.Name != "test.mkv" {
		t.Errorf("expected name 'test.mkv', got '%s'", file.Name)
	}
	if file.Size != 12345 {
		t.Errorf("expected size 12345, got %d", file.Size)
	}
	expectedTime := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)
	if !file.Modified.Equal(expectedTime) {
		t.Errorf("expected mod time %v, got %v", expectedTime, file.Modified)
	}
	if file.Offset != 32 {
		t.Errorf("expected offset 32, got %d", file.Offset)
	}
}

func TestParseHeader_Compressed(t *testing.T) {
	headerData := new(bytes.Buffer)
	headerData.WriteByte(kHeader)
	headerData.WriteByte(kMainStreamsInfo)
	headerData.WriteByte(kUnpackInfo)
	headerData.WriteByte(kFolder)
	headerData.Write(encodeNumber(1))
	headerData.WriteByte(0)
	headerData.Write(encodeNumber(1))
	headerData.WriteByte(0x03) // LZMA codec
	headerData.Write([]byte{0x03, 0x01, 0x01})
	headerData.WriteByte(kEnd)
	headerData.WriteByte(kEnd)

	_, err := parseHeader(headerData.Bytes())
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
