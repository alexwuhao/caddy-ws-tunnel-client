package proto

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// ErrUnsupportedVersion is returned when the header version byte != ProtocolVersion.
var ErrUnsupportedVersion = errors.New("tunnel proto: unsupported stream header version")

// ByteReader is the minimal interface ReadStreamHeader needs.
type ByteReader interface {
	io.Reader
	io.ByteReader
}

// ReadStreamHeader reads ver(1B) + uvarint portID from r.
// r must be a buffered reader so bytes after the header remain available.
func ReadStreamHeader(r ByteReader) (ver uint8, portID uint64, err error) {
	v, err := r.ReadByte()
	if err != nil {
		return 0, 0, fmt.Errorf("read version: %w", err)
	}
	if v != ProtocolVersion {
		return v, 0, ErrUnsupportedVersion
	}
	portID, err = binary.ReadUvarint(r)
	if err != nil {
		return v, 0, fmt.Errorf("read port id: %w", err)
	}
	return v, portID, nil
}

// NewHeaderReader wraps r in a bufio.Reader suitable for ReadStreamHeader.
// The returned reader must be used for all subsequent reads on the stream.
func NewHeaderReader(r io.Reader) *bufio.Reader {
	return bufio.NewReader(r)
}
