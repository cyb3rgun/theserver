package protocol

import (
	"bytes"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.Join(strings.Fields(s), ""))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mustData(t *testing.T, data any) []byte {
	t.Helper()
	raw, err := EncodeData(data)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func testID(n byte) EventID {
	var id EventID
	id[6] = 0x40
	id[8] = 0x80
	id[15] = n
	return id
}

func TestRoundTripEveryType(t *testing.T) {
	session := "s-1"
	messages := []Message{
		Hello{Dev: "tgt-01", FW: "1.2.3", Cls: ClassESP, Last: 41, Proto: Version},
		Welcome{Ack: 40, Now: 1_700_000_000_000, Ses: &session},
		Welcome{Ack: 0, Now: 1_700_000_000_000, Ses: nil},
		Event{ID: testID(1), Seq: 42, K: KindHit, Ts: 1_700_000_000_123,
			D: mustData(t, HitData{Ctl: "c-1", Cseq: 7, X: 0.25, Y: 0.75, Zone: "head", Pts: 100})},
		Ack{Seq: 42},
		Command{ID: 1, N: CommandSessionStart, A: map[string]any{"ses": "s-1", "scn": "range"}},
		Command{ID: 2, N: CommandReboot, A: map[string]any{}},
		Result{ID: 1, OK: true, R: map[string]any{"up": uint64(12)}},
		Result{ID: 2, OK: false, E: "unknown command"},
		Error{C: CodeSeqRegression, M: "device last 3 is below server ack 5"},
	}

	for _, msg := range messages {
		t.Run(msg.Type(), func(t *testing.T) {
			frame, err := Encode(msg)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			got, err := Decode(frame)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if got.Type() != msg.Type() {
				t.Fatalf("decoded a %s, want %s", got.Type(), msg.Type())
			}
			// Encoding the decoded message again must give the same bytes,
			// which proves every field survived the trip.
			again, err := Encode(got)
			if err != nil {
				t.Fatalf("Encode of the decoded message: %v", err)
			}
			if !bytes.Equal(frame, again) {
				t.Errorf("round trip changed the frame:\n first  %x\n second %x", frame, again)
			}
			if reflect.ValueOf(got).FieldByName("T").String() != msg.Type() {
				t.Errorf("decoded t is %q, want %q", reflect.ValueOf(got).FieldByName("T").String(), msg.Type())
			}
		})
	}
}

// TestHandWrittenHello guards the wire format against struct tag drift: the
// bytes below are written by hand from docs/protocol.md, not produced by the
// encoder.
func TestHandWrittenHello(t *testing.T) {
	// {"t":"hello","fw":"1.0","cls":"esp","dev":"tgt-01","last":0,"proto":1}
	// in Core Deterministic key order.
	canonical := mustHex(t, `
		a6
		61 74             65 68 65 6c 6c 6f
		62 66 77          63 31 2e 30
		63 63 6c 73       63 65 73 70
		63 64 65 76       66 74 67 74 2d 30 31
		64 6c 61 73 74    00
		65 70 72 6f 74 6f 01`)

	want := Hello{T: TypeHello, Dev: "tgt-01", FW: "1.0", Cls: "esp", Last: 0, Proto: 1}

	got, err := Decode(canonical)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != want {
		t.Errorf("decoded %+v, want %+v", got, want)
	}

	encoded, err := Encode(Hello{Dev: "tgt-01", FW: "1.0", Cls: "esp", Last: 0, Proto: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, canonical) {
		t.Errorf("Encode wrote\n %x\nwant the hand written\n %x", encoded, canonical)
	}

	// A device may send the keys in any order and add keys the server does
	// not know; both must decode the same way.
	// {"t":"hello","dev":"tgt-01","fw":"1.0","cls":"esp","last":7,"proto":1,"x":true}
	shuffled := mustHex(t, `
		a7
		61 74             65 68 65 6c 6c 6f
		63 64 65 76       66 74 67 74 2d 30 31
		62 66 77          63 31 2e 30
		63 63 6c 73       63 65 73 70
		64 6c 61 73 74    07
		65 70 72 6f 74 6f 01
		61 78             f5`)
	got, err = Decode(shuffled)
	if err != nil {
		t.Fatalf("Decode of the shuffled hello: %v", err)
	}
	want.Last = 7
	if got != want {
		t.Errorf("decoded %+v, want %+v", got, want)
	}
}

func TestWelcomeWithoutSessionSendsNull(t *testing.T) {
	frame, err := Encode(Welcome{Ack: 1, Now: 2})
	if err != nil {
		t.Fatal(err)
	}
	// "ses": null is 63 73 65 73 f6.
	if !bytes.Contains(frame, []byte{0x63, 0x73, 0x65, 0x73, 0xf6}) {
		t.Errorf("welcome without a session does not carry ses as null: %x", frame)
	}
	got, err := Decode(frame)
	if err != nil {
		t.Fatal(err)
	}
	if got.(Welcome).Ses != nil {
		t.Errorf("ses decoded as %q, want nil", *got.(Welcome).Ses)
	}
}

func TestEventDataIsKeptByteForByte(t *testing.T) {
	// A payload in an order the encoder would not choose, with a key the
	// server does not know: {"zz":1,"ctl":"c-9","cseq":3}.
	data := mustHex(t, `a3 62 7a 7a 01 63 63 74 6c 63 63 2d 39 64 63 73 65 71 03`)
	frame, err := Encode(Event{ID: testID(9), Seq: 1, K: KindShot, Ts: 5, D: data})
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(frame)
	if err != nil {
		t.Fatal(err)
	}
	ev := got.(Event)
	if !bytes.Equal(ev.D, data) {
		t.Errorf("d came back as %x, want %x", []byte(ev.D), data)
	}
	ctl, ok := ControllerID(ev.D)
	if !ok || ctl != "c-9" {
		t.Errorf("ControllerID is %q, %v, want c-9", ctl, ok)
	}

	var shot ShotData
	if err := DecodeData(ev.D, &shot); err != nil {
		t.Fatal(err)
	}
	if shot != (ShotData{Ctl: "c-9", Cseq: 3}) {
		t.Errorf("DecodeData gave %+v", shot)
	}
}

func TestControllerIDAbsent(t *testing.T) {
	raw := mustData(t, HealthData{Up: 10, RSSI: -60, Temp: 41.5, Free: 1024})
	if ctl, ok := ControllerID(raw); ok {
		t.Errorf("a health payload reported controller %q", ctl)
	}
}

func TestUnknownType(t *testing.T) {
	// {"t":"nope"}
	frame := mustHex(t, `a1 61 74 64 6e 6f 70 65`)
	if _, err := Decode(frame); !errors.Is(err, ErrUnknownType) {
		t.Errorf("Decode returned %v, want ErrUnknownType", err)
	}
}

func TestOversizedFrames(t *testing.T) {
	big := make([]byte, MaxFrameSize+1)
	big[0] = 0xa1
	if _, err := Decode(big); !errors.Is(err, ErrFrameTooLarge) {
		t.Errorf("Decode of %d bytes returned %v, want ErrFrameTooLarge", len(big), err)
	}

	type padded struct {
		Pad string `cbor:"pad"`
	}
	data := mustData(t, padded{Pad: strings.Repeat("x", MaxFrameSize)})
	_, err := Encode(Event{ID: testID(1), Seq: 1, K: KindState, Ts: 1, D: data})
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Errorf("Encode of an oversized event returned %v, want ErrFrameTooLarge", err)
	}
}

func TestMalformedFrames(t *testing.T) {
	tests := []struct {
		name  string
		frame string
	}{
		{name: "empty", frame: ``},
		{name: "not a map", frame: `83 01 02 03`},
		{name: "no type", frame: `a1 61 78 01`},
		{name: "type is not text", frame: `a1 61 74 01`},
		{name: "truncated", frame: `a2 61 74 62 65 76`},
		// {"t":"ack","t":"ack"}
		{name: "duplicate key", frame: `a2 61 74 63 61 63 6b 61 74 63 61 63 6b`},
		// {"t":"ack","seq":"one"}
		{name: "wrong field type", frame: `a2 61 74 63 61 63 6b 63 73 65 71 63 6f 6e 65`},
		// {"t":"ev","id":h'00..' of 15 bytes,"seq":1,"k":"shot","ts":1,"d":{}}
		{name: "event id of 15 bytes", frame: `a6 61 74 62 65 76 62 69 64 4f 000000000000400080000000000001
			63 73 65 71 01 61 6b 64 73 68 6f 74 62 74 73 01 61 64 a0`},
		// the same with 17 bytes
		{name: "event id of 17 bytes", frame: `a6 61 74 62 65 76 62 69 64 51 0000000000004000800000000000000101
			63 73 65 71 01 61 6b 64 73 68 6f 74 62 74 73 01 61 64 a0`},
		// seq 0
		{name: "event with seq 0", frame: `a6 61 74 62 65 76 62 69 64 50 00000000000040008000000000000001
			63 73 65 71 00 61 6b 64 73 68 6f 74 62 74 73 01 61 64 a0`},
		// d is an array
		{name: "event data not a map", frame: `a6 61 74 62 65 76 62 69 64 50 00000000000040008000000000000001
			63 73 65 71 01 61 6b 64 73 68 6f 74 62 74 73 01 61 64 80`},
		// no id at all
		{name: "event without id", frame: `a5 61 74 62 65 76
			63 73 65 71 01 61 6b 64 73 68 6f 74 62 74 73 01 61 64 a0`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Decode(mustHex(t, tt.frame))
			if !errors.Is(err, ErrBadMessage) {
				t.Errorf("Decode returned %v, want ErrBadMessage", err)
			}
		})
	}
}

func TestValidEventFrameByHand(t *testing.T) {
	// The same frame as the broken ones above, with a correct 16 byte id.
	frame := mustHex(t, `a6 61 74 62 65 76 62 69 64 50 00000000000040008000000000000001
		63 73 65 71 01 61 6b 64 73 68 6f 74 62 74 73 01 61 64 a0`)
	got, err := Decode(frame)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	ev := got.(Event)
	if ev.ID != testID(1) || ev.Seq != 1 || ev.K != KindShot || ev.Ts != 1 {
		t.Errorf("decoded %+v", ev)
	}
}

func TestNewEventID(t *testing.T) {
	seen := map[EventID]bool{}
	for range 1000 {
		id, err := NewEventID()
		if err != nil {
			t.Fatal(err)
		}
		if id.IsZero() || id[6]>>4 != 4 || id[8]>>6 != 2 {
			t.Fatalf("id %x is not a version 4 UUID", id)
		}
		if seen[id] {
			t.Fatalf("id %x drawn twice", id)
		}
		seen[id] = true
	}
}

func TestEncodeRejectsForeignTypes(t *testing.T) {
	type other struct{ Message }
	if _, err := Encode(other{}); err == nil {
		t.Error("Encode accepted a type that is not a protocol message")
	}
}
