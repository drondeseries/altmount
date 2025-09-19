package sevenzip

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"
)

var (
	// ErrNotSupported is returned when a 7z feature is not supported.
	ErrNotSupported = errors.New("not supported")
	// ErrCRCMismatch is returned when a CRC check fails.
	ErrCRCMismatch = errors.New("crc mismatch")
)

// Signature is the 7z file signature.
var Signature = []byte{'7', 'z', 0xBC, 0xAF, 0x27, 0x1C}

// StartHeader is the 7z start header.
type StartHeader struct {
	NextHeaderOffset uint64
	NextHeaderSize   uint64
	NextHeaderCRC    uint32
}

// ParseStartHeader parses the 7z start header from a reader.
func ParseStartHeader(r io.Reader) (*StartHeader, error) {
	var signature [6]byte
	if _, err := io.ReadFull(r, signature[:]); err != nil {
		return nil, err
	}

	if !bytes.Equal(signature[:], Signature) {
		return nil, errors.New("not a 7z file")
	}

	var version [2]byte
	if _, err := io.ReadFull(r, version[:]); err != nil {
		return nil, err
	}

	var startHeaderCRC uint32
	if err := binary.Read(r, binary.LittleEndian, &startHeaderCRC); err != nil {
		return nil, err
	}

	var h StartHeader
	if err := binary.Read(r, binary.LittleEndian, &h); err != nil {
		return nil, err
	}

	crc := crc32.NewIEEE()
	if err := binary.Write(crc, binary.LittleEndian, &h); err != nil {
		return nil, err
	}

	if crc.Sum32() != startHeaderCRC {
		return nil, ErrCRCMismatch
	}

	return &h, nil
}

// ReadNumber reads a 7z-encoded number.
func ReadNumber(r io.Reader) (uint64, error) {
	b, err := ReadByte(r)
	if err != nil {
		return 0, err
	}

	if b&0x80 == 0 {
		return uint64(b), nil
	}

	mask := byte(0x40)
	value := uint64(0)
	for i := 0; i < 8; i++ {
		if b&mask == 0 {
			value += uint64(b&^mask) << (8 * i)
			var nextByte byte
			for j := 0; j <= i; j++ {
				nextByte, err = ReadByte(r)
				if err != nil {
					return 0, err
				}
				value += uint64(nextByte) << (8 * (j + 1))
			}
			return value, nil
		}
		mask >>= 1
	}

	return 0, errors.New("invalid number")
}

// ReadByte reads a single byte from a reader.
func ReadByte(r io.Reader) (byte, error) {
	var b [1]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, err
	}
	return b[0], nil
}

func parseHeader(r io.Reader) (*ArchiveInfo, int64, error) {
	id, err := ReadByte(r)
	if err != nil {
		return nil, 0, err
	}

	if id != 0x01 {
		return nil, 0, errors.New("invalid header id")
	}

	var packPos int64
	var files []FileEntry

	for {
		id, err := ReadByte(r)
		if err != nil {
			return nil, 0, err
		}
		if id == 0x00 { // End
			break
		}

		switch id {
		case 0x04: // MainStreamsInfo
			packInfo, err := parsePackInfo(r)
			if err != nil {
				return nil, 0, err
			}
			packPos = int64(packInfo.PackPos)

			err = parseUnpackInfo(r, packInfo.NumPackStreams)
			if err != nil {
				return nil, 0, err
			}

		case 0x05: // FilesInfo
			files, err = parseFilesInfo(r)
			if err != nil {
				return nil, 0, err
			}

		default:
			if err := skipProperty(r); err != nil {
				return nil, 0, err
			}
		}
	}

	return &ArchiveInfo{Files: files}, packPos, nil
}

type packInfo struct {
	PackPos        uint64
	NumPackStreams uint64
}

func parsePackInfo(r io.Reader) (*packInfo, error) {
	id, err := ReadByte(r)
	if err != nil {
		return nil, err
	}
	if id != 0x06 {
		return nil, errors.New("invalid pack info id")
	}

	packPos, err := ReadNumber(r)
	if err != nil {
		return nil, err
	}

	numPackStreams, err := ReadNumber(r)
	if err != nil {
		return nil, err
	}

	var pi = &packInfo{
		PackPos:        packPos,
		NumPackStreams: numPackStreams,
	}

	id, err = ReadByte(r)
	if err != nil {
		return nil, err
	}

	if id == 0x09 { // Size
		for i := uint64(0); i < numPackStreams; i++ {
			if _, err := ReadNumber(r); err != nil {
				return nil, err
			}
		}
		id, err = ReadByte(r)
		if err != nil {
			return nil, err
		}
	}

	if id == 0x0A { // CRC
		// Skip CRCs
		if err := skipProperty(r); err != nil {
			return nil, err
		}
		id, err = ReadByte(r)
		if err != nil {
			return nil, err
		}
	}

	if id != 0x00 {
		return nil, errors.New("invalid pack info end")
	}

	return pi, nil
}

func parseUnpackInfo(r io.Reader, numPackStreams uint64) error {
	id, err := ReadByte(r)
	if err != nil {
		return err
	}
	if id != 0x07 {
		return errors.New("invalid unpack info id")
	}

	id, err = ReadByte(r)
	if err != nil {
		return err
	}
	if id != 0x0B { // Folder
		return errors.New("invalid folder id")
	}

	numFolders, err := ReadNumber(r)
	if err != nil {
		return err
	}

	external, err := ReadByte(r)
	if err != nil {
		return err
	}
	if external != 0 {
		return ErrNotSupported
	}

	for i := uint64(0); i < numFolders; i++ {
		numCoders, err := ReadNumber(r)
		if err != nil {
			return err
		}

		if numCoders != 1 {
			return ErrNotSupported
		}

		flags, err := ReadByte(r)
		if err != nil {
			return err
		}

		codecIDSize := flags & 0x0F
		isComplex := (flags & 0x10) != 0
		hasAttributes := (flags & 0x20) != 0

		if isComplex {
			return ErrNotSupported
		}

		codecID := make([]byte, codecIDSize)
		if _, err := io.ReadFull(r, codecID); err != nil {
			return err
		}

		if !bytes.Equal(codecID, []byte{0x00}) { // Not Copy method
			return ErrNotSupported
		}

		if hasAttributes {
			return ErrNotSupported
		}
	}

	id, err = ReadByte(r)
	if err != nil {
		return err
	}
	if id != 0x0C { // CodersUnpackSize
		return errors.New("invalid coders unpack size id")
	}

	for i := uint64(0); i < numFolders; i++ {
		if _, err := ReadNumber(r); err != nil {
			return err
		}
	}

	id, err = ReadByte(r)
	if err != nil {
		return err
	}
	if id == 0x0A { // CRC
		if err := skipProperty(r); err != nil {
			return err
		}
		id, err = ReadByte(r)
		if err != nil {
			return err
		}
	}

	if id != 0x00 { // End
		return errors.New("invalid unpack info end")
	}

	return nil
}

func parseFilesInfo(r io.Reader) ([]FileEntry, error) {
	numFiles, err := ReadNumber(r)
	if err != nil {
		return nil, err
	}

	files := make([]FileEntry, numFiles)
	var emptyStreamMask []bool
	var emptyFileMask []bool
	var antiMask []bool

	for {
		id, err := ReadByte(r)
		if err != nil {
			return nil, err
		}
		if id == 0x00 { // End
			break
		}

		size, err := ReadNumber(r)
		if err != nil {
			return nil, err
		}
		p := make([]byte, size)
		if _, err := io.ReadFull(r, p); err != nil {
			return nil, err
		}
		pr := bytes.NewReader(p)

		switch id {
		case 0x11: // Name
			allDefined, err := ReadByte(pr)
			if err != nil {
				return nil, err
			}
			if allDefined != 0 {
				return nil, ErrNotSupported
			}
			external, err := ReadByte(pr)
			if err != nil {
				return nil, err
			}
			if external != 0 {
				return nil, ErrNotSupported
			}
			for i := uint64(0); i < numFiles; i++ {
				var name []byte
				for {
					c, err := ReadByte(pr)
					if err != nil {
						return nil, err
					}
					c2, err := ReadByte(pr)
					if err != nil {
						return nil, err
					}
					if c == 0 && c2 == 0 {
						break
					}
					name = append(name, c, c2)
				}
				files[i].Name = string(name)
			}
		case 0x0E: // EmptyStream
			emptyStreamMask, err = readBoolVector(pr, int(numFiles))
			if err != nil {
				return nil, err
			}
		case 0x0F: // EmptyFile
			emptyFileMask, err = readBoolVector(pr, int(numFiles))
			if err != nil {
				return nil, err
			}
		case 0x10: // Anti
			antiMask, err = readBoolVector(pr, int(numFiles))
			if err != nil {
				return nil, err
			}
		default:
			// ignore other properties
		}
	}

	var unpackSizes []uint64
	var unpackCRCs []uint32
	var crcsDefined []bool

	// This is a simplified implementation that assumes a single folder.
	// A more complete implementation would need to parse the substreams info.
	// For now, we'll just read the unpack sizes.
	id, err := ReadByte(r)
	if err != nil {
		return nil, err
	}
	if id == 0x08 { // SubStreamsInfo
		for {
			id, err := ReadByte(r)
			if err != nil {
				return nil, err
			}
			if id == 0x00 { // End
				break
			}
			size, err := ReadNumber(r)
			if err != nil {
				return nil, err
			}
			p := make([]byte, size)
			if _, err := io.ReadFull(r, p); err != nil {
				return nil, err
			}
			pr := bytes.NewReader(p)
			switch id {
			case 0x09: // Size
				for i := uint64(0); i < numFiles; i++ {
					if emptyStreamMask != nil && emptyStreamMask[i] {
						continue
					}
					size, err := ReadNumber(pr)
					if err != nil {
						return nil, err
					}
					unpackSizes = append(unpackSizes, size)
				}
			case 0x0A: // CRC
				crcsDefined, err = readBoolVector(pr, len(unpackSizes))
				if err != nil {
					return nil, err
				}
				for i := 0; i < len(unpackSizes); i++ {
					if crcsDefined[i] {
						var crc uint32
						if err := binary.Read(pr, binary.LittleEndian, &crc); err != nil {
							return nil, err
						}
						unpackCRCs = append(unpackCRCs, crc)
					}
				}
			}
		}
	}

	sizeIndex := 0
	for i := uint64(0); i < numFiles; i++ {
		if emptyStreamMask != nil && emptyStreamMask[i] {
			files[i].Size = 0
		} else {
			files[i].Size = unpackSizes[sizeIndex]
			sizeIndex++
		}
	}

	return files, nil
}

func readBoolVector(r io.Reader, n int) ([]bool, error) {
	allDefined, err := ReadByte(r)
	if err != nil {
		return nil, err
	}
	if allDefined == 1 {
		v := make([]bool, n)
		for i := 0; i < n; i++ {
			v[i] = true
		}
		return v, nil
	}

	b := make([]byte, (n+7)/8)
	if _, err := io.ReadFull(r, b); err != nil {
		return nil, err
	}

	v := make([]bool, n)
	for i := 0; i < n; i++ {
		v[i] = (b[i/8] & (0x80 >> (i % 8))) != 0
	}
	return v, nil
}

func skipProperty(r io.Reader) error {
	size, err := ReadNumber(r)
	if err != nil {
		return err
	}
	if _, err := io.CopyN(io.Discard, r, int64(size)); err != nil {
		return err
	}
	return nil
}
