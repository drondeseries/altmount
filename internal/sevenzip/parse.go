package sevenzip

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"
	"unicode/utf16"
)

const (
	signatureHeaderSize = 32
)

var (
	signature = []byte{'7', 'z', 0xBC, 0xAF, 0x27, 0x1C}

	errInvalidSignature      = errors.New("invalid 7z signature")
	errUnsupportedVersion    = errors.New("unsupported 7z version")
	errCompressedHeader      = errors.New("compressed headers are not supported")
	errUnsupportedCodec      = errors.New("unsupported or compressed archive, only 'Copy' method is supported")
	errInvalidHeaderFormat   = errors.New("invalid or corrupt header format")
	errFilesInfoMissing      = errors.New("files information is missing from the archive header")
	errPackInfoMissing       = errors.New("pack information is missing from the archive header")
	errFolderInfoMissing     = errors.New("folder information is missing from the archive header")
	errSizesMissing          = errors.New("file sizes are missing")
	errUnsupportedProperties = errors.New("archive uses unsupported properties")
)

// Property IDs
const (
	kEnd                   = 0x00
	kHeader                = 0x01
	kArchiveProperties     = 0x02
	kAdditionalStreamsInfo = 0x03
	kMainStreamsInfo       = 0x04
	kFilesInfo             = 0x05
	kPackInfo              = 0x06
	kUnpackInfo            = 0x07
	kSubStreamsInfo        = 0x08
	kSize                  = 0x09
	kCRC                   = 0x0a
	kFolder                = 0x0b
	kCodersUnpackSize      = 0x0c
	kNumUnpackStream       = 0x0d
	kEmptyStream           = 0x0e
	kEmptyFile             = 0x0f
	kAnti                  = 0x10
	kName                  = 0x11
	kCTime                 = 0x12
	kATime                 = 0x13
	kMTime                 = 0x14
	kWinAttributes         = 0x15
	kEncodedHeader         = 0x17
)

type startHeader struct {
	NextHeaderOffset uint64
	NextHeaderSize   uint64
	NextHeaderCRC    uint32
}

func parse(r io.ReaderAt, size int64) (*ArchiveInfo, error) {
	buf := make([]byte, signatureHeaderSize)
	if _, err := r.ReadAt(buf, 0); err != nil {
		return nil, fmt.Errorf("failed to read signature header: %w", err)
	}

	if !bytes.Equal(buf[0:6], signature) {
		return nil, errInvalidSignature
	}

	if buf[6] != 0 || buf[7] != 4 {
		return nil, errUnsupportedVersion
	}

	var sh startHeader
	sh.NextHeaderOffset = binary.LittleEndian.Uint64(buf[12:20])
	sh.NextHeaderSize = binary.LittleEndian.Uint64(buf[20:28])

	headerOffset := int64(signatureHeaderSize) + int64(sh.NextHeaderOffset)
	headerData := make([]byte, sh.NextHeaderSize)
	if _, err := r.ReadAt(headerData, headerOffset); err != nil {
		return nil, fmt.Errorf("failed to read header data: %w", err)
	}

	return parseHeader(headerData)
}

func parseHeader(data []byte) (*ArchiveInfo, error) {
	br := bytes.NewReader(data)

	propID, err := br.ReadByte()
	if err != nil || propID != kHeader {
		return nil, errInvalidHeaderFormat
	}

	var packPos uint64
	var files []FileEntry
	var unpackSizes []uint64

	for {
		propID, err := br.ReadByte()
		if err == io.EOF || propID == kEnd {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("error reading property ID: %w", err)
		}

		switch propID {
		case kMainStreamsInfo:
			packPos, unpackSizes, err = parseMainStreamsInfo(br)
			if err != nil {
				return nil, err
			}
		case kFilesInfo:
			files, err = parseFilesInfo(br, unpackSizes)
			if err != nil {
				return nil, err
			}
		case kArchiveProperties, kAdditionalStreamsInfo:
			if err := skipProperty(br); err != nil {
				return nil, fmt.Errorf("failed to skip property 0x%x: %w", propID, err)
			}
		case kEncodedHeader:
			return nil, errCompressedHeader
		default:
			return nil, fmt.Errorf("unexpected property ID in header: 0x%x", propID)
		}
	}

	if files == nil {
		return nil, errFilesInfoMissing
	}

	baseOffset := uint64(signatureHeaderSize) + packPos
	var currentOffset uint64
	for i := range files {
		files[i].Offset = baseOffset + currentOffset
		if files[i].Size > 0 {
			currentOffset += files[i].Size
		}
	}

	return &ArchiveInfo{Files: files}, nil
}

func parseMainStreamsInfo(br *bytes.Reader) (uint64, []uint64, error) {
	var packPos uint64
	var unpackSizes []uint64

	for {
		propID, err := br.ReadByte()
		if err == io.EOF || propID == kEnd {
			break
		}
		if err != nil {
			return 0, nil, err
		}

		switch propID {
		case kPackInfo:
			packPos, err = parsePackInfo(br)
			if err != nil {
				return 0, nil, err
			}
		case kUnpackInfo:
			unpackSizes, err = parseUnpackInfo(br)
			if err != nil {
				return 0, nil, err
			}
		case kSubStreamsInfo:
			unpackSizes, err = parseSubStreamsInfo(br)
			if err != nil {
				return 0, nil, err
			}
		default:
			return 0, nil, fmt.Errorf("unexpected property in MainStreamsInfo: 0x%x", propID)
		}
	}

	return packPos, unpackSizes, nil
}

func parsePackInfo(br *bytes.Reader) (uint64, error) {
	packPos, err := readNumber(br)
	if err != nil {
		return 0, err
	}

	numPackStreams, err := readNumber(br)
	if err != nil {
		return 0, err
	}

	for {
		propID, err := br.ReadByte()
		if err == io.EOF || propID == kEnd {
			break
		}
		if err != nil {
			return 0, err
		}

		switch propID {
		case kSize:
			for i := uint64(0); i < numPackStreams; i++ {
				if _, err := readNumber(br); err != nil {
					return 0, err
				}
			}
		case kCRC:
			if err := skipProperty(br); err != nil {
				return 0, err
			}
		default:
			return 0, fmt.Errorf("unexpected property in PackInfo: 0x%x", propID)
		}
	}

	return packPos, nil
}

func parseUnpackInfo(br *bytes.Reader) ([]uint64, error) {
	propID, err := br.ReadByte()
	if err != nil || propID != kFolder {
		return nil, errFolderInfoMissing
	}

	numFolders, err := readNumber(br)
	if err != nil {
		return nil, err
	}

	if b, err := br.ReadByte(); err != nil || b != 0 {
		return nil, errUnsupportedProperties
	}

	for i := uint64(0); i < numFolders; i++ {
		numCoders, err := readNumber(br)
		if err != nil {
			return nil, err
		}
		if numCoders != 1 {
			return nil, errUnsupportedCodec
		}

		flags, err := br.ReadByte()
		if err != nil {
			return nil, err
		}
		codecIDSize := flags & 0x0F
		if (flags & 0x10) != 0 {
			return nil, errUnsupportedCodec
		}

		codecID := make([]byte, codecIDSize)
		if _, err := io.ReadFull(br, codecID); err != nil {
			return nil, err
		}

		if !bytes.Equal(codecID, []byte{0x00}) {
			return nil, errUnsupportedCodec
		}

		if (flags & 0x20) != 0 {
			propSize, err := readNumber(br)
			if err != nil {
				return nil, err
			}
			if _, err := br.Seek(int64(propSize), io.SeekCurrent); err != nil {
				return nil, err
			}
		}
	}

	propID, err = br.ReadByte()
	if err != nil || propID != kCodersUnpackSize {
		return nil, errSizesMissing
	}

	var unpackSizes []uint64
	for i := uint64(0); i < numFolders; i++ {
		size, err := readNumber(br)
		if err != nil {
			return nil, err
		}
		unpackSizes = append(unpackSizes, size)
	}

	for {
		propID, err := br.ReadByte()
		if err == io.EOF || propID == kEnd {
			break
		}
		if err != nil {
			return nil, err
		}
		if err := skipProperty(br); err != nil {
			return nil, err
		}
	}

	return unpackSizes, nil
}

func parseSubStreamsInfo(br *bytes.Reader) ([]uint64, error) {
	var unpackSizes []uint64
	for {
		propID, err := br.ReadByte()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if propID == kEnd {
			break
		}

		switch propID {
		case kNumUnpackStream, kCRC:
			if err := skipProperty(br); err != nil {
				return nil, err
			}
		case kSize:
			propSize, err := readNumber(br)
			if err != nil {
				return nil, err
			}
			propData := make([]byte, propSize)
			if _, err := io.ReadFull(br, propData); err != nil {
				return nil, fmt.Errorf("failed to read size property data: %w", err)
			}
			subBr := bytes.NewReader(propData)
			for subBr.Len() > 0 {
				size, err := readNumber(subBr)
				if err != nil {
					if err == io.EOF {
						break
					}
					return nil, fmt.Errorf("failed to read size number: %w", err)
				}
				unpackSizes = append(unpackSizes, size)
			}
		default:
			if err := skipProperty(br); err != nil {
				return nil, err
			}
		}
	}
	return unpackSizes, nil
}

func parseFilesInfo(br *bytes.Reader, unpackSizes []uint64) ([]FileEntry, error) {
	numFiles, err := readNumber(br)
	if err != nil {
		return nil, err
	}

	files := make([]FileEntry, numFiles)
	var emptyStreamMask []bool
	filesWithContentCount := int(numFiles)

	for {
		propID, err := br.ReadByte()
		if err == io.EOF || propID == kEnd {
			break
		}
		if err != nil {
			return nil, err
		}

		switch propID {
		case kName:
			if err := parseNames(br, files); err != nil {
				return nil, err
			}
		case kEmptyStream:
			size, err := readNumber(br)
			if err != nil {
				return nil, err
			}
			data := make([]byte, size)
			if _, err := io.ReadFull(br, data); err != nil {
				return nil, err
			}
			emptyStreamMask, err = readBoolList(bytes.NewReader(data), int(numFiles))
			if err != nil {
				return nil, err
			}
			filesWithContentCount = int(numFiles) - countTrue(emptyStreamMask)
		case kMTime:
			if err := parseMTime(br, files); err != nil {
				return nil, err
			}
		case kEmptyFile, kWinAttributes, kCTime, kATime:
			if err := skipProperty(br); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("unexpected property in FilesInfo: 0x%x", propID)
		}
	}

	if len(unpackSizes) > 0 {
		if filesWithContentCount != len(unpackSizes) {
			// Mismatch can happen, e.g. in solid archives. Trust SubStreamsInfo.
		}
		sizeIndex := 0
		for i := 0; i < int(numFiles); i++ {
			if emptyStreamMask == nil || !emptyStreamMask[i] {
				if sizeIndex < len(unpackSizes) {
					files[i].Size = unpackSizes[sizeIndex]
					sizeIndex++
				}
			}
		}
	}

	return files, nil
}

func parseNames(br *bytes.Reader, files []FileEntry) error {
	size, err := readNumber(br)
	if err != nil {
		return err
	}
	data := make([]byte, size)
	if _, err := io.ReadFull(br, data); err != nil {
		return err
	}
	propReader := bytes.NewReader(data)

	if b, err := propReader.ReadByte(); err != nil || b != 0 {
		return errUnsupportedProperties
	}

	fileIndex := 0
	for fileIndex < len(files) {
		var nameBuf bytes.Buffer
		for {
			char := make([]byte, 2)
			if _, err := io.ReadFull(propReader, char); err != nil {
				return err
			}
			if char[0] == 0 && char[1] == 0 {
				break
			}
			nameBuf.Write(char)
		}
		utf16Chars := make([]uint16, nameBuf.Len()/2)
		for j := 0; j < len(utf16Chars); j++ {
			utf16Chars[j] = binary.LittleEndian.Uint16(nameBuf.Bytes()[j*2:])
		}
		files[fileIndex].Name = string(utf16.Decode(utf16Chars))
		fileIndex++
	}

	return nil
}

func parseMTime(br *bytes.Reader, files []FileEntry) error {
	size, err := readNumber(br)
	if err != nil {
		return err
	}
	data := make([]byte, size)
	if _, err := io.ReadFull(br, data); err != nil {
		return err
	}
	propReader := bytes.NewReader(data)

	defined, err := readBoolList(propReader, len(files))
	if err != nil {
		return err
	}
	if b, err := propReader.ReadByte(); err != nil || b != 0 {
		return errUnsupportedProperties
	}
	for i := 0; i < len(files); i++ {
		if defined[i] {
			winFileTime, err := readNumber(propReader)
			if err != nil {
				return err
			}
			unixEpoch := int64((winFileTime / 10000000) - 11644473600)
			files[i].Modified = time.Unix(unixEpoch, 0)
		}
	}
	return nil
}

func readNumber(r io.ByteReader) (uint64, error) {
	firstByte, err := r.ReadByte()
	if err != nil {
		return 0, err
	}

	mask := byte(0x80)
	value := uint64(0)
	numBytes := 0

	for i := 0; i < 8; i++ {
		if (firstByte & mask) == 0 {
			highBits := uint64(firstByte & (mask - 1))
			value = highBits
			numBytes = i
			break
		}
		mask >>= 1
	}

	for i := 0; i < numBytes; i++ {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		value |= uint64(b) << (8 * (i + 1))
	}

	return value, nil
}

func readBoolList(r *bytes.Reader, numItems int) ([]bool, error) {
	allDefined, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	if allDefined == 1 {
		list := make([]bool, numItems)
		for i := range list {
			list[i] = true
		}
		return list, nil
	}

	list := make([]bool, numItems)
	var currentByte byte
	var mask byte = 0
	for i := 0; i < numItems; i++ {
		if mask == 0 {
			currentByte, err = r.ReadByte()
			if err != nil {
				return nil, err
			}
			mask = 0x80
		}
		if (currentByte & mask) != 0 {
			list[i] = true
		}
		mask >>= 1
	}
	return list, nil
}

func skipProperty(br *bytes.Reader) error {
	size, err := readNumber(br)
	if err != nil {
		return err
	}
	_, err = br.Seek(int64(size), io.SeekCurrent)
	return err
}

func countTrue(s []bool) int {
	count := 0
	for _, v := range s {
		if v {
			count++
		}
	}
	return count
}
