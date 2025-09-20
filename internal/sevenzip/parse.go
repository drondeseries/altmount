package sevenzip

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"
	"unicode/utf16"

	"github.com/ulikunitz/xz/lzma"
)

const (
	signatureHeaderSize = 32
)

var (
	signature = []byte{'7', 'z', 0xBC, 0xAF, 0x27, 0x1C}

	errInvalidSignature      = errors.New("invalid 7z signature")
	errUnsupportedVersion    = errors.New("unsupported 7z version")
	errUnsupportedCodec      = errors.New("unsupported or compressed archive, only 'Copy' method is supported for files")
	errInvalidHeaderFormat   = errors.New("invalid or corrupt header format")
	errFilesInfoMissing      = errors.New("files information is missing from the archive header")
	errPackInfoMissing       = errors.New("pack information is missing from the archive header")
	errFolderInfoMissing     = errors.New("folder information is missing from the archive header")
	errSizesMissing          = errors.New("file sizes are missing")
	errUnsupportedProperties = errors.New("archive uses unsupported properties")
)

// Property IDs from 7zFormat.txt
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
	kComment               = 0x16
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
	sh.NextHeaderCRC = binary.LittleEndian.Uint32(buf[8:12])

	headerOffset := int64(signatureHeaderSize)
	headerData := make([]byte, sh.NextHeaderSize)
	if _, err := r.ReadAt(headerData, headerOffset+int64(sh.NextHeaderOffset)); err != nil {
		return nil, fmt.Errorf("failed to read header data: %w", err)
	}

	// TODO: verify headerData CRC

	return parseHeader(r, headerData, headerOffset)
}

func parseHeader(r io.ReaderAt, data []byte, baseOffset int64) (*ArchiveInfo, error) {
	br := bytes.NewReader(data)
	propID, err := br.ReadByte()
	if err != nil {
		return nil, errInvalidHeaderFormat
	}

	switch propID {
	case kHeader:
		return parseDecodedHeader(br, baseOffset)
	case kEncodedHeader:
		return parseEncodedHeader(r, br, baseOffset)
	default:
		return nil, errInvalidHeaderFormat
	}
}

func parseEncodedHeader(r io.ReaderAt, br *bytes.Reader, baseOffset int64) (*ArchiveInfo, error) {
	streamsInfo, err := parseStreamsInfo(br)
	if err != nil {
		return nil, fmt.Errorf("failed to parse streams info for encoded header: %w", err)
	}

	if streamsInfo.PackInfo == nil || len(streamsInfo.PackInfo.PackSizes) == 0 {
		return nil, errors.New("no pack info found for encoded header")
	}
	if streamsInfo.UnpackInfo == nil || len(streamsInfo.UnpackInfo.Folders) == 0 {
		return nil, errors.New("no folder info found for encoded header")
	}

	folder := streamsInfo.UnpackInfo.Folders[0]
	if len(folder.Coders) != 1 || !bytes.Equal(folder.Coders[0].CodecID, []byte{0x03, 0x01, 0x01}) {
		return nil, fmt.Errorf("unsupported codec for header decompression: %x", folder.Coders[0].CodecID)
	}

	packStreamOffset := baseOffset + int64(streamsInfo.PackInfo.PackPos)
	packSize := int64(streamsInfo.PackInfo.PackSizes[0])
	compressedStreamReader := io.NewSectionReader(r, packStreamOffset, packSize)

	coder := folder.Coders[0]
	if len(coder.Properties) < 1 {
		return nil, errors.New("not enough properties for lzma")
	}

	// Construct a fake LZMA header.
	// See https://www.7-zip.org/7z.html for format.
	// 1 byte properties, 4 bytes dict size, 8 bytes uncompressed size.
	fakeHeader := make([]byte, 13)
	// Properties byte
	fakeHeader[0] = coder.Properties[0]
	// Dictionary size (4 bytes, little-endian)
	if len(coder.Properties) < 5 {
		return nil, errors.New("not enough properties for lzma dict size")
	}
	copy(fakeHeader[1:5], coder.Properties[1:5])
	// Uncompressed size (8 bytes, little-endian)
	if len(folder.UnpackSizes) == 0 {
		return nil, errors.New("missing unpack size for header")
	}
	binary.LittleEndian.PutUint64(fakeHeader[5:13], folder.UnpackSizes[0])

	// Create a multireader that reads the fake header first, then the real data.
	multiReader := io.MultiReader(bytes.NewReader(fakeHeader), compressedStreamReader)

	lzmaReader, err := lzma.NewReader(multiReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create lzma reader: %w", err)
	}

	decompressedHeader, err := io.ReadAll(lzmaReader)
	if err != nil {
		return nil, fmt.Errorf("failed to decompress header: %w", err)
	}

	return parseHeader(r, decompressedHeader, baseOffset)
}

func parseDecodedHeader(br *bytes.Reader, baseOffset int64) (*ArchiveInfo, error) {
	var streamsInfo *StreamsInfo
	var filesInfo *FilesInfo

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
			streamsInfo, err = parseStreamsInfo(br)
			if err != nil {
				return nil, err
			}
		case kFilesInfo:
			filesInfo, err = parseFilesInfo(br)
			if err != nil {
				return nil, err
			}
		case kArchiveProperties, kAdditionalStreamsInfo:
			if err := skipProperty(br); err != nil {
				return nil, fmt.Errorf("failed to skip property 0x%x: %w", propID, err)
			}
		default:
			return nil, fmt.Errorf("unexpected property ID in header: 0x%x", propID)
		}
	}

	if filesInfo == nil {
		return nil, errFilesInfoMissing
	}
	if streamsInfo == nil || streamsInfo.PackInfo == nil {
		return nil, errPackInfoMissing
	}
	if streamsInfo.UnpackInfo == nil {
		return nil, errFolderInfoMissing
	}

	// For streamable archives, ensure no compression is used for file data
	for _, folder := range streamsInfo.UnpackInfo.Folders {
		for _, coder := range folder.Coders {
			if !bytes.Equal(coder.CodecID, []byte{0x00}) {
				return nil, errUnsupportedCodec
			}
		}
	}

	// Combine information to build final FileEntry list
	files := make([]FileEntry, filesInfo.NumFiles)
	unpackSizes := streamsInfo.SubStreamsInfo.UnpackSizes
	sizeIndex := 0
	for i := 0; i < int(filesInfo.NumFiles); i++ {
		files[i].Name = filesInfo.Names[i]
		files[i].Modified = filesInfo.MTime[i]
		if filesInfo.EmptyStreamMask == nil || !filesInfo.EmptyStreamMask[i] {
			if sizeIndex < len(unpackSizes) {
				files[i].Size = unpackSizes[sizeIndex]
				sizeIndex++
			}
		}
	}

	packPos := streamsInfo.PackInfo.PackPos
	archiveBaseOffset := baseOffset + int64(packPos)
	var currentOffset uint64
	for i := range files {
		files[i].Offset = uint64(archiveBaseOffset) + currentOffset
		if files[i].Size > 0 {
			currentOffset += files[i].Size
		}
	}

	return &ArchiveInfo{Files: files}, nil
}

type StreamsInfo struct {
	PackInfo       *PackInfo
	UnpackInfo     *UnpackInfo
	SubStreamsInfo *SubStreamsInfo
}

func parseStreamsInfo(br *bytes.Reader) (*StreamsInfo, error) {
	info := &StreamsInfo{}

	for {
		propID, err := br.ReadByte()
		if err == io.EOF || propID == kEnd {
			break
		}
		if err != nil {
			return nil, err
		}

		switch propID {
		case kPackInfo:
			info.PackInfo, err = parsePackInfo(br)
			if err != nil {
				return nil, err
			}
		case kUnpackInfo:
			info.UnpackInfo, err = parseUnpackInfo(br)
			if err != nil {
				return nil, err
			}
		case kSubStreamsInfo:
			info.SubStreamsInfo, err = parseSubStreamsInfo(br)
			if err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("unexpected property in StreamsInfo: 0x%x", propID)
		}
	}

	return info, nil
}

func parsePackInfo(br *bytes.Reader) (*PackInfo, error) {
	pi := &PackInfo{}
	var err error

	pi.PackPos, err = readNumber(br)
	if err != nil {
		return nil, err
	}

	pi.NumPackStreams, err = readNumber(br)
	if err != nil {
		return nil, err
	}

	for {
		propID, err := br.ReadByte()
		if err == io.EOF || propID == kEnd {
			break
		}
		if err != nil {
			return nil, err
		}

		switch propID {
		case kSize:
			pi.PackSizes = make([]uint64, pi.NumPackStreams)
			for i := uint64(0); i < pi.NumPackStreams; i++ {
				pi.PackSizes[i], err = readNumber(br)
				if err != nil {
					return nil, err
				}
			}
		case kCRC:
			// Skipping CRC for now
			if err := skipProperty(br); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("unexpected property in PackInfo: 0x%x", propID)
		}
	}

	return pi, nil
}

func parseUnpackInfo(br *bytes.Reader) (*UnpackInfo, error) {
	propID, err := br.ReadByte()
	if err != nil || propID != kFolder {
		return nil, errFolderInfoMissing
	}

	numFolders, err := readNumber(br)
	if err != nil {
		return nil, err
	}
	ui := &UnpackInfo{Folders: make([]Folder, numFolders)}

	if b, err := br.ReadByte(); err != nil || b != 0 {
		return nil, errUnsupportedProperties
	}

	for i := uint64(0); i < numFolders; i++ {
		folder := &ui.Folders[i]
		numCoders, err := readNumber(br)
		if err != nil {
			return nil, err
		}
		folder.Coders = make([]CoderInfo, numCoders)
		for j := uint64(0); j < numCoders; j++ {
			coder := &folder.Coders[j]
			flags, err := br.ReadByte()
			if err != nil {
				return nil, err
			}
			codecIDSize := flags & 0x0F
			coder.CodecID = make([]byte, codecIDSize)
			if _, err := io.ReadFull(br, coder.CodecID); err != nil {
				return nil, err
			}

			if (flags & 0x20) != 0 {
				propSize, err := readNumber(br)
				if err != nil {
					return nil, err
				}
				coder.Properties = make([]byte, propSize)
				if _, err := io.ReadFull(br, coder.Properties); err != nil {
					return nil, err
				}
			}
		}
	}

	propID, err = br.ReadByte()
	if err != nil || propID != kCodersUnpackSize {
		return nil, errSizesMissing
	}

	for i := uint64(0); i < numFolders; i++ {
		folder := &ui.Folders[i]
		folder.UnpackSizes = make([]uint64, 1) // Assuming one unpack size per folder for now
		folder.UnpackSizes[0], err = readNumber(br)
		if err != nil {
			return nil, err
		}
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

	return ui, nil
}

func parseSubStreamsInfo(br *bytes.Reader) (*SubStreamsInfo, error) {
	ssi := &SubStreamsInfo{}
	numUnpackStreams := uint64(1) // Default if not specified

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
			numUnpackStreams, err = readNumber(br)
			if err != nil {
				return nil, err
			}
		case kSize:
			ssi.UnpackSizes = make([]uint64, numUnpackStreams)
			for i := uint64(0); i < numUnpackStreams; i++ {
				ssi.UnpackSizes[i], err = readNumber(br)
				if err != nil {
					return nil, err
				}
			}
		case kCRC:
			if err := skipProperty(br); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("unexpected property in SubStreamsInfo: 0x%x", propID)
		}
	}
	return ssi, nil
}

type FilesInfo struct {
	NumFiles        uint64
	Names           []string
	EmptyStreamMask []bool
	MTime           []time.Time
}

func parseFilesInfo(br *bytes.Reader) (*FilesInfo, error) {
	numFiles, err := readNumber(br)
	if err != nil {
		return nil, err
	}
	fi := &FilesInfo{
		NumFiles: numFiles,
		Names:    make([]string, numFiles),
		MTime:    make([]time.Time, numFiles),
	}

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
			size, err := readNumber(br)
			if err != nil {
				return nil, err
			}
			data := make([]byte, size)
			if _, err := io.ReadFull(br, data); err != nil {
				return nil, err
			}
			if err := parseNames(bytes.NewReader(data), fi.Names); err != nil {
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
			fi.EmptyStreamMask, err = readBoolList(bytes.NewReader(data), int(fi.NumFiles))
			if err != nil {
				return nil, err
			}
		case kMTime:
			size, err := readNumber(br)
			if err != nil {
				return nil, err
			}
			data := make([]byte, size)
			if _, err := io.ReadFull(br, data); err != nil {
				return nil, err
			}
			if err := parseMTime(bytes.NewReader(data), fi.MTime); err != nil {
				return nil, err
			}
		case kWinAttributes, kCTime, kATime, kEmptyFile, kAnti:
			if err := skipProperty(br); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("unexpected property in FilesInfo: 0x%x", propID)
		}
	}
	return fi, nil
}

func parseNames(propReader *bytes.Reader, names []string) error {
	if b, err := propReader.ReadByte(); err != nil || b != 0 {
		return errUnsupportedProperties
	}
	fileIndex := 0
	for fileIndex < len(names) {
		var nameBuf bytes.Buffer
		for {
			char := make([]byte, 2)
			if _, err := io.ReadFull(propReader, char); err != nil {
				if err == io.EOF {
					break
				}
				return err
			}
			if char[0] == 0 && char[1] == 0 {
				break
			}
			nameBuf.Write(char)
		}
		if nameBuf.Len() == 0 && propReader.Len() == 0 {
			break
		}
		utf16Chars := make([]uint16, nameBuf.Len()/2)
		for j := 0; j < len(utf16Chars); j++ {
			utf16Chars[j] = binary.LittleEndian.Uint16(nameBuf.Bytes()[j*2:])
		}
		names[fileIndex] = string(utf16.Decode(utf16Chars))
		fileIndex++
	}
	return nil
}

func parseMTime(propReader *bytes.Reader, mtimes []time.Time) error {
	defined, err := readBoolList(propReader, len(mtimes))
	if err != nil {
		return err
	}
	if b, err := propReader.ReadByte(); err != nil || b != 0 {
		return errUnsupportedProperties
	}
	for i := 0; i < len(mtimes); i++ {
		if defined[i] {
			winFileTime, err := readNumber(propReader)
			if err != nil {
				return err
			}
			unixEpoch := int64((winFileTime / 10000000) - 11644473600)
			mtimes[i] = time.Unix(unixEpoch, 0)
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
