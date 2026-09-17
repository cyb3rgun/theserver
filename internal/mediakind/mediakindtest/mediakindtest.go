// Package mediakindtest builds the smallest files that carry a container
// and a codec, for the tests of the packages that take media: a few boxes,
// a few EBML elements, one Ogg page, a PNG signature. Nothing here plays;
// it is enough for mediakind to name the kind.
package mediakindtest

import (
	"bytes"
	"encoding/binary"
)

// Box builds one MP4 box.
func Box(typ string, payload ...[]byte) []byte {
	body := bytes.Join(payload, nil)
	b := make([]byte, 8, 8+len(body))
	binary.BigEndian.PutUint32(b[:4], uint32(8+len(body)))
	copy(b[4:8], typ)
	return append(b, body...)
}

// MP4 builds a file with one sample entry of format, the movie box behind
// the media data, where a file written in one pass carries it.
func MP4(format string) []byte {
	entries := []byte{0, 0, 0, 0, 0, 0, 0, 1} // version and flags, one entry
	stsd := Box("stsd", entries, Box(format, make([]byte, 70)))
	return bytes.Join([][]byte{
		Box("ftyp", []byte("isomiso2avc1mp41")),
		Box("mdat", []byte("not a real picture")),
		Box("moov", Box("trak", Box("mdia", Box("minf", Box("stbl", stsd))))),
	}, nil)
}

// EBML builds one element with a four byte size.
func EBML(id uint64, payload ...[]byte) []byte {
	body := bytes.Join(payload, nil)
	var head []byte
	for shift := 56; shift >= 0; shift -= 8 {
		if b := byte(id >> shift); b != 0 || len(head) > 0 {
			head = append(head, b)
		}
	}
	size := make([]byte, 4)
	binary.BigEndian.PutUint32(size, uint32(len(body)))
	size[0] |= 0x10
	return append(append(head, size...), body...)
}

// WebM builds a file with a doc type and one track; trackType is 1 for a
// picture and 2 for sound.
func WebM(docType, codec string, trackType byte) []byte {
	return bytes.Join([][]byte{
		EBML(0x1a45dfa3, EBML(0x4282, []byte(docType))),
		EBML(0x18538067, EBML(0x1654ae6b, EBML(0xae,
			EBML(0x83, []byte{trackType}),
			EBML(0x86, []byte(codec))))),
	}, nil)
}

// Ogg builds a first page with one packet.
func Ogg(packet []byte) []byte {
	page := make([]byte, 28)
	copy(page, "OggS")
	page[26] = 1
	page[27] = byte(len(packet))
	return append(page, packet...)
}

// The files an editor draft takes, one per use.
func Clip() []byte    { return MP4("avc1") }
func Overlay() []byte { return WebM("webm", "V_VP9", 1) }
func Sound() []byte   { return Ogg([]byte("OpusHead\x01\x02")) }

// Cover is a PNG head; theserver reads the signature, not the picture.
func Cover() []byte {
	return []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 13, 'I', 'H', 'D', 'R'}
}
