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
	errUnpackInfoMissing     = errors.New("unpack information is missing from the archive header")
	errFolderInfoMissing     = errors.New("folder information is missing from the archive header")
	errNamesMissing          = errors.New("file names are missing")
	errSizesMissing          = errors.New("file sizes are missing")
	errMTimeMissing          = errors.New("file modification times are missing")
	errUnsupportedProperties = errors.New("archive uses unsupported properties")
)

// Property IDs
const (
	kEnd = 0x00
	kHeader = 0x01
	kArchiveProperties = 0x02
	kAdditionalStreamsInfo = 0x03
	kMainStreamsInfo = 0x04
	kFilesInfo = 0x05
	kPackInfo = 0x06
	kUnpackInfo = 0x07
	kSubStreamsInfo = 0x08
	kSize = 0x09
	kCRC = 0x0a
	kFolder = 0x0b
	kCodersUnpackSize = 0x0c
	kNumUnpackStream = 0x0d
	kEmptyStream = 0x0e
	kEmptyFile = 0x0f
	kAnti = 0x10
	kName = 0x11
	kCTime = 0x12
	kATime = 0x13
	kMTime = 0x14
	kWinAttributes = 0x15
	kComment = 0x16
	kEncodedHeader = 0x17
	kStartPos = 0x18
	kDummy = 0x19
)

type startHeader struct {
	NextHeaderOffset uint64
	NextHeaderSize   uint64
	NextHeaderCRC    uint32
}

func parse(r io.ReaderAt, size int64) (*ArchiveInfo, error) {
	// 1. Read and verify signature header
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
	sh.NextHeaderCRC = binary.LittleEndian.Uint32(buf[28:32])

	// 2. Locate and read the main header
	headerOffset := int64(signatureHeaderSize) + int64(sh.NextHeaderOffset)
	headerData := make([]byte, sh.NextHeaderSize)
	if _, err := r.ReadAt(headerData, headerOffset); err != nil {
		return nil, fmt.Errorf("failed to read header data: %w", err)
	}

	// 3. Parse the header data
	return parseHeader(headerData)
}

// parseHeader is the main parsing function for the 7z header data.
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
			var err error
			packPos, unpackSizes, err = parseMainStreamsInfo(br)
			if err != nil {
				return nil, err
			}
		case kFilesInfo:
			var err error
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
	if packPos == 0 && len(files) > 0 {
		// packPos can be 0 if there are no files with content
		hasContent := false
		for _, f := range files {
			if f.Size > 0 {
				hasContent = true
				break
			}
		}
		if hasContent {
			return nil, errPackInfoMissing
		}
	}

	// Calculate absolute offsets for each file
	baseOffset := uint64(signatureHeaderSize) + packPos
	var currentOffset uint64
	for i := range files {
		files[i].Offset = baseOffset + currentOffset
		// Only increment the offset for files that have content
		if files[i].Size > 0 {
			currentOffset += files[i].Size
		}
	}

	return &ArchiveInfo{Files: files}, nil
}

// parseMainStreamsInfo parses the MainStreamsInfo block, which contains information
// about how the files are packed and what compression they use.
func parseMainStreamsInfo(br *bytes.Reader) (uint64, []uint64, error) {
	var packPos uint64
	var unpackSizes []uint64
	var err error

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
			// This provides detailed unpack sizes for solid archives.
			// Let's parse it properly.
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

// parseUnpackInfo validates that the archive is uncompressed and extracts the total unpack size for each folder.
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
		return nil, errUnsupportedProperties // External flag must be 0
	}

	for i := uint64(0); i < numFolders; i++ {
		numCoders, err := readNumber(br)
		if err != nil {
			return nil, err
		}
		if numCoders != 1 {
			return nil, errUnsupportedCodec // Only support simple, single-coder folders
		}

		flags, err := br.ReadByte()
		if err != nil {
			return nil, err
		}
		codecIDSize := flags & 0x0F
		isComplex := (flags & 0x10) != 0
		hasAttributes := (flags & 0x20) != 0

		if isComplex {
			return nil, errUnsupportedCodec
		}

		codecID := make([]byte, codecIDSize)
		if _, err := io.ReadFull(br, codecID); err != nil {
			return nil, err
		}

		// Crucially, only the COPY codec (ID 0x00) is supported for streaming.
		if !bytes.Equal(codecID, []byte{0x00}) {
			return nil, errUnsupportedCodec
		}

		if hasAttributes {
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
	// The spec is a bit unclear, but it seems there's one unpack size per folder's output stream.
	// For simple Copy codec, this means one per folder.
	for i := uint64(0); i < numFolders; i++ {
		size, err := readNumber(br)
		if err != nil {
			return nil, err
		}
		unpackSizes = append(unpackSizes, size)
	}

	// Read until the end of the UnpackInfo block
	for {
		propID, err := br.ReadByte()
		if err == io.EOF || propID == kEnd {
			break
		}
		if err != nil {
			return nil, err
		}
		// Skip unknown properties like CRC
		if err := skipProperty(br); err != nil {
			return nil, err
		}
	}

	return unpackSizes, nil
}

// parseSubStreamsInfo extracts the unpack sizes for each file in a solid archive.
func parseSubStreamsInfo(br *bytes.Reader) ([]uint64, error) {
	// This property is optional, but if present, it gives us the exact file sizes.
	var unpackSizes []uint64

	// The structure can be complex. We only care about the kSize property.
	for {
		propID, err := br.ReadByte()
		if err == io.EOF || propID == kEnd {
			break
		}
		if err != nil {
			return nil, err
		}

		switch propID {
		case kNumUnpackStream:
			// Number of streams per folder. Skip it.
			if err := skipProperty(br); err != nil {
				return nil, err
			}
		case kSize:
			// This is what we want. A list of sizes for all the files.
			// We don't know the number of files yet, so we read until the property ends.
			// This is risky. Let's assume the outer loop handles the property size.
			endPos := br.Len()
			// The property size was already read by the calling loop, so we subtract what's left
			// from the original size of the property block.
			// This is getting complicated. Let's re-read the property block.
			propSize, err := readNumber(br)
			if err != nil {
				return nil, err
			}

			// We need to read 'propSize' bytes of sizes.
			// Let's create a sub-reader
			subBr := bytes.NewReader(make([]byte, propSize))
			if _, err := io.CopyN(subBr, br, int64(propSize)); err != nil {
				return nil, err
			}
			subBr.Seek(0, io.SeekStart)

			for subBr.Len() > 0 {
				size, err := readNumber(subBr)
				if err != nil {
					// This can happen if we read past the end of the numbers
					if err == io.EOF {
						break
					}
					return nil, err
				}
				unpackSizes = append(unpackSizes, size)
			}

		case kCRC:
			// Skip CRC information
			if err := skipProperty(br); err != nil {
				return nil, err
			}
		default:
			if err := skipProperty(br); err != nil {
				return nil, err
			}
		}

	}
	return unpackSizes, nil
}

// parseFilesInfo parses the file metadata, including names, times, and attributes.
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
			if err != nil { return nil, err }
			data := make([]byte, size)
			if _, err := io.ReadFull(br, data); err != nil { return nil, err }

			emptyStreamMask, err = readBoolList(bytes.NewReader(data), int(numFiles))
			if err != nil { return nil, err }

			filesWithContentCount = int(numFiles) - countTrue(emptyStreamMask)

		case kEmptyFile:
			// This indicates which of the non-empty streams are actually empty files (size 0).
			// We can just skip this for now.
			if err := skipProperty(br); err != nil { return nil, err }
		case kMTime:
			if err := parseMTime(br, files); err != nil {
				return nil, err
			}
		case kWinAttributes, kCTime, kATime, kDummy, kAnti:
			if err := skipProperty(br); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("unexpected property in FilesInfo: 0x%x", propID)
		}
	}

	// Assign sizes to files that are not empty streams.
	if len(unpackSizes) > 0 {
		if filesWithContentCount != len(unpackSizes) {
			// This can happen with solid archives. The unpackSizes from kUnpackInfo might just have one entry for the whole solid block.
			// The sizes from kSubStreamsInfo are the ones to trust.
		}
		sizeIndex := 0
		for i := 0; i < int(numFiles); i++ {
			if emptyStreamMask != nil && i < len(emptyStreamMask) && emptyStreamMask[i] {
				files[i].Size = 0
			} else {
				if sizeIndex < len(unpackSizes) {
					files[i].Size = unpackSizes[sizeIndex]
					sizeIndex++
				} else {
					// It's possible to have more files than unpack sizes if some are empty.
					// This case should be handled by the emptyStreamMask. If we're here, something is wrong.
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
		return errUnsupportedProperties // External flag
	}

	nameBuffer := make([]byte, (len(data)-1)*2) // UTF-16 can be larger than UTF-8
	if _, err := io.ReadFull(propReader, nameBuffer); err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return err
	}

	fileIndex := 0
	start := 0
	for i := 0; i+1 < len(nameBuffer); i += 2 {
		if nameBuffer[i] == 0 && nameBuffer[i+1] == 0 {
			if fileIndex >= len(files) {
				return errors.New("more names than files in header")
			}
			utf16Chars := make([]uint16, (i-start)/2)
			for j := 0; j < len(utf16Chars); j++ {
				utf16Chars[j] = binary.LittleEndian.Uint16(nameBuffer[start+(j*2):])
			}
			files[fileIndex].Name = string(utf16.Decode(utf16Chars))
			fileIndex++
			start = i + 2
		}
	}

	return nil
}

func parseMTime(br *bytes.Reader, files []FileEntry) error {
	size, err := readNumber(br)
	if err != nil { return err }
	data := make([]byte, size)
	if _, err := io.ReadFull(br, data); err != nil { return err }
	propReader := bytes.NewReader(data)

	defined, err := readBoolList(propReader, len(files))
	if err != nil {
		return err
	}
	if b, err := propReader.ReadByte(); err != nil || b != 0 {
		return errUnsupportedProperties // Must not be external
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


// --- Utility functions ---

// readNumber reads a variable-length integer used in 7z headers.
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
			value = uint64(firstByte & (mask - 1))
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
