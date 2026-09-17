// Package mediakind tells the container and the codec of a media file from
// its header alone (D-045). It decodes nothing and needs nothing outside the
// standard library: it walks the boxes of an MP4, the elements of a WebM,
// the first page of an Ogg stream, and reads the signature of a PNG.
//
// The editor uses it at upload, so a file a target cannot play is refused
// where the person can still do something about it.
package mediakind

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
)

// The containers this reads.
const (
	MP4  = "mp4"
	WebM = "webm"
	Ogg  = "ogg"
	PNG  = "png"
)

// The codecs this names. Anything else is reported by the name the file
// gives it, so a message can say what was found.
const (
	H264   = "h264"
	AV1    = "av1"
	VP8    = "vp8"
	VP9    = "vp9"
	Vorbis = "vorbis"
	Opus   = "opus"
	Image  = "png"
)

// The uses a file can have in a scenario package. Every use takes its own
// kinds; the list comes from docs/scenario.md section 6, settled with the
// architect for S01-B08.
const (
	// UseClip is the video of a scenario: media.main, a media state, the
	// background of a layered scenario.
	UseClip = "clip"
	// UseOverlay is the blood overlay of the immediate reaction, which needs
	// alpha.
	UseOverlay = "overlay"
	// UseSound is the impact sound.
	UseSound = "sound"
	// UseCover is cover.png of the package.
	UseCover = "cover"
)

// A Kind is what a file holds.
type Kind struct {
	Container string
	Codec     string
}

func (k Kind) String() string {
	if k.Codec == "" {
		return k.Container
	}
	return k.Container + " with " + k.Codec
}

// ErrUnknown says the file is none of the containers this reads.
var ErrUnknown = errors.New("not a media file theserver knows")

// accepted maps every use to the kinds it takes.
var accepted = map[string][]Kind{
	UseClip:    {{MP4, H264}, {MP4, AV1}},
	UseOverlay: {{WebM, VP8}, {WebM, VP9}, {WebM, AV1}},
	UseSound:   {{Ogg, Vorbis}, {Ogg, Opus}},
	UseCover:   {{PNG, Image}},
}

// Uses lists the uses, in the order a page shows them.
func Uses() []string {
	return []string{UseClip, UseOverlay, UseSound, UseCover}
}

// Accepted lists the kinds a use takes.
func Accepted(use string) []Kind {
	return slices.Clone(accepted[use])
}

// UseOf returns the use a kind can serve, or an empty string when no use
// takes it.
func UseOf(k Kind) string {
	for _, use := range Uses() {
		if slices.Contains(accepted[use], k) {
			return use
		}
	}
	return ""
}

// Of reads the head of the file and tells its kind. The kind may name a
// codec no use takes; UseOf says so.
func Of(r io.ReaderAt, size int64) (Kind, error) {
	head := make([]byte, 16)
	n, _ := r.ReadAt(head, 0)
	if n < 12 {
		return Kind{}, ErrUnknown
	}
	switch {
	case bytes.HasPrefix(head, []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}):
		return Kind{PNG, Image}, nil
	case bytes.Equal(head[4:8], []byte("ftyp")):
		return mp4Kind(r, size)
	case bytes.HasPrefix(head, []byte{0x1a, 0x45, 0xdf, 0xa3}):
		return webmKind(r, size)
	case bytes.HasPrefix(head, []byte("OggS")):
		return oggKind(r)
	}
	return Kind{}, ErrUnknown
}

// OfFile is Of for a file on disk.
func OfFile(name string) (Kind, error) {
	f, err := os.Open(name)
	if err != nil {
		return Kind{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Kind{}, err
	}
	return Of(f, info.Size())
}

// maxElements stops a walk through a file whose sizes send it in circles.
const maxElements = 4096

// A box is one MP4 box: its type and where its payload lies.
type box struct {
	typ  string
	off  int64
	size int64
}

// boxes calls fn for every box between off and end, until fn stops it.
func boxes(r io.ReaderAt, off, end int64, fn func(b box) (bool, error)) error {
	for n := 0; off+8 <= end; n++ {
		if n > maxElements {
			return errors.New("too many boxes")
		}
		header := make([]byte, 8)
		if _, err := r.ReadAt(header, off); err != nil {
			return err
		}
		size := int64(binary.BigEndian.Uint32(header[:4]))
		payload := off + 8
		if size == 1 {
			ext := make([]byte, 8)
			if _, err := r.ReadAt(ext, off+8); err != nil {
				return err
			}
			size = int64(binary.BigEndian.Uint64(ext))
			payload = off + 16
		}
		if size == 0 {
			size = end - off
		}
		if size < payload-off || off+size > end {
			return fmt.Errorf("the box %q at %d says %d bytes", header[4:8], off, size)
		}
		stop, err := fn(box{string(header[4:8]), payload, off + size - payload})
		if err != nil || stop {
			return err
		}
		off += size
	}
	return nil
}

// mp4Codecs maps the format of a sample entry to a codec name.
var mp4Codecs = map[string]string{
	"avc1": H264, "avc3": H264, "avc4": H264,
	"av01": AV1, "vp08": VP8, "vp09": VP9,
	"hvc1": "h265", "hev1": "h265",
	"mp4a": "aac", "Opus": Opus, "ac-3": "ac3", "fLaC": "flac",
}

// mp4Kind finds the sample description of the first track that carries a
// codec this knows; a video codec wins over an audio one.
func mp4Kind(r io.ReaderAt, size int64) (Kind, error) {
	kind := Kind{Container: MP4}
	var descend func(off, end int64, path []string) error
	descend = func(off, end int64, path []string) error {
		return boxes(r, off, end, func(b box) (bool, error) {
			switch {
			case b.typ == "stsd" && len(path) > 0 && path[len(path)-1] == "stbl":
				return false, sampleEntries(r, b, &kind)
			case slices.Contains([]string{"moov", "trak", "mdia", "minf", "stbl"}, b.typ):
				return false, descend(b.off, b.off+b.size, append(path, b.typ))
			}
			return false, nil
		})
	}
	// A file that stops early keeps what was found; the caller refuses a
	// kind without a codec.
	descend(0, size, nil)
	return kind, nil
}

// videoCodecs are the codecs of a picture; one of them ends the search.
var videoCodecs = []string{H264, AV1, VP8, VP9, "h265"}

func sampleEntries(r io.ReaderAt, b box, kind *Kind) error {
	if b.size < 8 {
		return errors.New("a sample description without entries")
	}
	header := make([]byte, 8)
	if _, err := r.ReadAt(header, b.off); err != nil {
		return err
	}
	count := int(binary.BigEndian.Uint32(header[4:8]))
	off := b.off + 8
	for i := 0; i < count && i < maxElements && off+8 <= b.off+b.size; i++ {
		entry := make([]byte, 8)
		if _, err := r.ReadAt(entry, off); err != nil {
			return err
		}
		size := int64(binary.BigEndian.Uint32(entry[:4]))
		format := string(entry[4:8])
		if codec, ok := mp4Codecs[format]; ok {
			if kind.Codec == "" || slices.Contains(videoCodecs, codec) {
				kind.Codec = codec
			}
			if slices.Contains(videoCodecs, codec) {
				return nil
			}
		}
		if size < 8 {
			return fmt.Errorf("a sample entry of %d bytes", size)
		}
		off += size
	}
	return nil
}

// The EBML element ids a WebM file is read for.
const (
	idEBML       = 0x1a45dfa3
	idDocType    = 0x4282
	idSegment    = 0x18538067
	idTracks     = 0x1654ae6b
	idTrackEntry = 0xae
	idTrackType  = 0x83
	idCodecID    = 0x86
)

// webmCodecs maps the CodecID of a track to a codec name.
var webmCodecs = map[string]string{
	"V_VP8": VP8, "V_VP9": VP9, "V_AV1": AV1, "V_AV01": AV1,
	"A_OPUS": Opus, "A_VORBIS": Vorbis,
}

// vint reads an EBML variable length integer at off. With marker the length
// bits stay in the value, which is how element ids are written.
func vint(r io.ReaderAt, off int64, marker bool) (value uint64, length int64, err error) {
	first := make([]byte, 1)
	if _, err := r.ReadAt(first, off); err != nil {
		return 0, 0, err
	}
	length = 1
	mask := byte(0x80)
	for length <= 8 && first[0]&mask == 0 {
		length++
		mask >>= 1
	}
	if length > 8 {
		return 0, 0, errors.New("not an EBML number")
	}
	rest := make([]byte, length)
	if _, err := r.ReadAt(rest, off); err != nil {
		return 0, 0, err
	}
	value = uint64(rest[0])
	if !marker {
		value = uint64(rest[0] & (mask - 1))
	}
	for _, b := range rest[1:] {
		value = value<<8 | uint64(b)
	}
	return value, length, nil
}

// elements calls fn for every EBML element between off and end.
func elements(r io.ReaderAt, off, end int64, fn func(id uint64, payload, size int64) (bool, error)) error {
	for n := 0; off < end; n++ {
		if n > maxElements {
			return errors.New("too many elements")
		}
		id, idLen, err := vint(r, off, true)
		if err != nil {
			return err
		}
		size, sizeLen, err := vint(r, off+idLen, false)
		if err != nil {
			return err
		}
		payload := off + idLen + sizeLen
		// A size of all ones means the element runs to the end.
		if size >= uint64(1)<<(7*sizeLen)-1 || payload+int64(size) > end {
			size = uint64(end - payload)
		}
		stop, err := fn(id, payload, int64(size))
		if err != nil || stop {
			return err
		}
		off = payload + int64(size)
	}
	return nil
}

// webmKind reads the DocType and the CodecID of the first track with a
// picture, else of the first track at all.
func webmKind(r io.ReaderAt, size int64) (Kind, error) {
	kind := Kind{Container: WebM}
	text := func(payload, length int64) string {
		if length < 0 || length > 64 {
			return ""
		}
		buf := make([]byte, length)
		if _, err := r.ReadAt(buf, payload); err != nil {
			return ""
		}
		return strings.TrimRight(string(buf), "\x00")
	}
	elements(r, 0, size, func(id uint64, payload, length int64) (bool, error) {
		switch id {
		case idEBML:
			return false, elements(r, payload, payload+length, func(id uint64, payload, length int64) (bool, error) {
				if id == idDocType {
					if doc := text(payload, length); doc != "webm" {
						kind.Container = doc
					}
					return true, nil
				}
				return false, nil
			})
		case idSegment:
			return true, elements(r, payload, payload+length, func(id uint64, payload, length int64) (bool, error) {
				if id != idTracks {
					return false, nil
				}
				return true, elements(r, payload, payload+length, func(id uint64, payload, length int64) (bool, error) {
					if id != idTrackEntry {
						return false, nil
					}
					video, codec := false, ""
					err := elements(r, payload, payload+length, func(id uint64, payload, length int64) (bool, error) {
						switch id {
						case idCodecID:
							codec = webmCodecs[text(payload, length)]
						case idTrackType:
							one := make([]byte, 1)
							r.ReadAt(one, payload)
							video = length == 1 && one[0] == 1
						}
						return false, nil
					})
					if codec != "" && (kind.Codec == "" || video) {
						kind.Codec = codec
					}
					return video && codec != "", err
				})
			})
		}
		return false, nil
	})
	return kind, nil
}

// oggKind reads the first packet of the first page, which every Ogg stream
// starts with its identification header.
func oggKind(r io.ReaderAt) (Kind, error) {
	header := make([]byte, 27)
	if _, err := r.ReadAt(header, 0); err != nil {
		return Kind{}, ErrUnknown
	}
	segments := int64(header[26])
	packet := make([]byte, 8)
	n, _ := r.ReadAt(packet, 27+segments)
	packet = packet[:n]
	kind := Kind{Container: Ogg}
	switch {
	case bytes.HasPrefix(packet, []byte("OpusHead")):
		kind.Codec = Opus
	case bytes.HasPrefix(packet, append([]byte{1}, "vorbis"...)):
		kind.Codec = Vorbis
	case bytes.HasPrefix(packet, []byte{0x7f, 'F', 'L', 'A', 'C'}):
		kind.Codec = "flac"
	case bytes.HasPrefix(packet, append([]byte{0x80}, "theora"...)):
		kind.Codec = "theora"
	}
	return kind, nil
}
