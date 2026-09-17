package mediakind_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cyb3rgun/theserver/internal/mediakind"
	"github.com/cyb3rgun/theserver/internal/mediakind/mediakindtest"
)

var (
	box  = mediakindtest.Box
	mp4  = mediakindtest.MP4
	webm = mediakindtest.WebM
	ogg  = mediakindtest.Ogg
	png  = mediakindtest.Cover()
)

func TestOfReadsContainerAndCodec(t *testing.T) {
	cases := []struct {
		name      string
		data      []byte
		container string
		codec     string
		use       string
	}{
		{"h264 in mp4", mp4("avc1"), "mp4", "h264", mediakind.UseClip},
		{"another h264 entry", mp4("avc3"), "mp4", "h264", mediakind.UseClip},
		{"av1 in mp4", mp4("av01"), "mp4", "av1", mediakind.UseClip},
		{"h265 in mp4", mp4("hvc1"), "mp4", "h265", ""},
		{"sound in mp4", mp4("mp4a"), "mp4", "aac", ""},
		{"an unknown entry", mp4("mjpg"), "mp4", "", ""},
		{"vp9 in webm", webm("webm", "V_VP9", 1), "webm", "vp9", mediakind.UseOverlay},
		{"vp8 in webm", webm("webm", "V_VP8", 1), "webm", "vp8", mediakind.UseOverlay},
		{"av1 in webm", webm("webm", "V_AV1", 1), "webm", "av1", mediakind.UseOverlay},
		{"sound in webm", webm("webm", "A_OPUS", 2), "webm", "opus", ""},
		{"matroska", webm("matroska", "V_VP9", 1), "matroska", "vp9", ""},
		{"opus in ogg", ogg([]byte("OpusHead\x01\x02")), "ogg", "opus", mediakind.UseSound},
		{"vorbis in ogg", ogg(append([]byte{1}, "vorbis\x00"...)), "ogg", "vorbis", mediakind.UseSound},
		{"theora in ogg", ogg(append([]byte{0x80}, "theora"...)), "ogg", "theora", ""},
		{"a png", png, "png", "png", mediakind.UseCover},
	}
	for _, c := range cases {
		kind, err := mediakind.Of(bytes.NewReader(c.data), int64(len(c.data)))
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if kind.Container != c.container || kind.Codec != c.codec {
			t.Errorf("%s reads as %+v, want %s %s", c.name, kind, c.container, c.codec)
		}
		if got := mediakind.UseOf(kind); got != c.use {
			t.Errorf("%s serves %q, want %q", c.name, got, c.use)
		}
	}
}

func TestOfRefusesWhatItCannotRead(t *testing.T) {
	long := bytes.Repeat([]byte("not a media file, only text\n"), 8)
	broken := box("ftyp", []byte("isom"))
	broken = append(broken, 0xff, 0xff, 0xff, 0xf0, 'm', 'o', 'o', 'v') // a size past the end
	for _, c := range []struct {
		name string
		data []byte
		err  bool
	}{
		{"nothing", nil, true},
		{"too short", []byte("OggS"), true},
		{"plain text", long, true},
		{"a zip", append([]byte("PK\x03\x04"), long...), true},
		{"an mp4 with a box past the end", broken, false},
	} {
		kind, err := mediakind.Of(bytes.NewReader(c.data), int64(len(c.data)))
		switch {
		case c.err && !errors.Is(err, mediakind.ErrUnknown):
			t.Errorf("%s gave %+v, %v", c.name, kind, err)
		case !c.err && err != nil:
			t.Errorf("%s gave %v", c.name, err)
		case !c.err && mediakind.UseOf(kind) != "":
			t.Errorf("%s serves %q", c.name, mediakind.UseOf(kind))
		}
	}
}

func TestUsesAndKinds(t *testing.T) {
	if len(mediakind.Uses()) != 4 {
		t.Fatalf("the uses are %v", mediakind.Uses())
	}
	for _, use := range mediakind.Uses() {
		kinds := mediakind.Accepted(use)
		if len(kinds) == 0 {
			t.Errorf("the use %s takes no kind", use)
		}
		for _, k := range kinds {
			if got := mediakind.UseOf(k); got != use {
				t.Errorf("%s is listed for %s but serves %q", k, use, got)
			}
			if k.String() == "" {
				t.Errorf("the kind %+v has no name", k)
			}
		}
	}
}

func TestOfFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "clip.mp4")
	if err := os.WriteFile(file, mp4("av01"), 0o600); err != nil {
		t.Fatal(err)
	}
	kind, err := mediakind.OfFile(file)
	if err != nil || kind.Container != "mp4" || kind.Codec != "av1" {
		t.Fatalf("OfFile gave %+v, %v", kind, err)
	}
	if _, err := mediakind.OfFile(filepath.Join(t.TempDir(), "none.mp4")); err == nil {
		t.Error("a missing file gave no error")
	}
}
