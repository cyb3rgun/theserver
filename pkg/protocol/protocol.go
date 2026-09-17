// Package protocol is the wire format of the device link, version 1, as
// docs/protocol.md defines it: one CBOR map per binary WebSocket frame, with
// the message type under the key t and short ASCII keys for everything else.
//
// Encode writes Core Deterministic CBOR (RFC 8949 section 4.2.1), so the same
// message always produces the same bytes. Decode switches on t, enforces the
// frame size limit, rejects duplicate map keys and ignores unknown keys.
package protocol

import (
	"crypto/rand"
	"errors"
	"fmt"
	"reflect"

	"github.com/fxamacker/cbor/v2"
)

// Version is the protocol version a hello must carry.
const Version = 1

// MaxFrameSize is the largest frame either side may send, in bytes.
const MaxFrameSize = 4096

// Message types, the value of t.
const (
	TypeHello   = "hello"
	TypeWelcome = "welcome"
	TypeEvent   = "ev"
	TypeAck     = "ack"
	TypeCommand = "cmd"
	TypeResult  = "res"
	TypeError   = "err"
)

// Error codes, the value of c in an err message.
const (
	CodeUnknownType      = "unknown_type"
	CodeBadMessage       = "bad_message"
	CodeSeqRegression    = "seq_regression"
	CodeUnauthorized     = "unauthorized"
	CodeProtoUnsupported = "proto_unsupported"
)

// Device classes, the value of cls in a hello.
const (
	ClassESP = "esp"
	ClassPi  = "pi"
	ClassPC  = "pc"
)

// Event kinds of S01, the value of k in an ev message.
const (
	KindShot   = "shot"
	KindHit    = "hit"
	KindMiss   = "miss"
	KindState  = "state"
	KindHealth = "health"
	// KindContent reports the install state of a scenario version
	// (protocol section 8.10).
	KindContent = "content"
)

// The states of a content event.
const (
	ContentInstalling = "installing"
	ContentInstalled  = "installed"
	ContentFailed     = "failed"
	ContentRemoved    = "removed"
)

// Command names of S01, the value of n in a cmd message.
const (
	CommandTimeMark     = "time_mark"
	CommandSessionStart = "session_start"
	CommandSessionStop  = "session_stop"
	CommandSetConfig    = "set_config"
	CommandReboot       = "reboot"
	// CommandContentAvailable announces a scenario version the device
	// should fetch (protocol section 8.10); its arguments are
	// ContentAvailable.
	CommandContentAvailable = "content_available"
)

var (
	// ErrUnknownType is a frame whose t names no message of this version.
	ErrUnknownType = errors.New("unknown message type")
	// ErrFrameTooLarge is a frame above MaxFrameSize.
	ErrFrameTooLarge = fmt.Errorf("frame larger than %d bytes", MaxFrameSize)
	// ErrBadMessage is a frame that is not a well formed message.
	ErrBadMessage = errors.New("malformed message")
)

// A Message is one of the message types below.
type Message interface {
	// Type is the value of t for this message.
	Type() string
}

// Hello is the first message of a device after it connects.
type Hello struct {
	T     string `cbor:"t"`
	Dev   string `cbor:"dev"`
	FW    string `cbor:"fw"`
	Cls   string `cbor:"cls"`
	Last  uint64 `cbor:"last"`
	Proto uint64 `cbor:"proto"`
}

// Welcome answers a hello. Ses is nil when no session is running, and is
// then sent as CBOR null. Ep is the sequence epoch of the device, added in
// S01-B04 (protocol section 8.8); a device whose journal belongs to another
// epoch starts over at seq 1. It is 0 when a server does not send it.
type Welcome struct {
	T   string  `cbor:"t"`
	Ack uint64  `cbor:"ack"`
	Now int64   `cbor:"now"`
	Ses *string `cbor:"ses"`
	Ep  uint64  `cbor:"ep,omitempty"`
}

// Event is one journal entry of a device. D is the kind specific map exactly
// as it arrived, so the server can store it byte for byte.
type Event struct {
	T   string          `cbor:"t"`
	ID  EventID         `cbor:"id"`
	Seq uint64          `cbor:"seq"`
	K   string          `cbor:"k"`
	Ts  int64           `cbor:"ts"`
	D   cbor.RawMessage `cbor:"d"`
}

// Ack is the cumulative acknowledgement: every seq up to Seq is stored.
type Ack struct {
	T   string `cbor:"t"`
	Seq uint64 `cbor:"seq"`
}

// Command is sent by the server. ID is an unsigned integer, counted per
// connection from 1.
type Command struct {
	T  string         `cbor:"t"`
	ID uint64         `cbor:"id"`
	N  string         `cbor:"n"`
	A  map[string]any `cbor:"a"`
}

// Result answers a command with the same ID. E and R are left out when
// empty.
type Result struct {
	T  string         `cbor:"t"`
	ID uint64         `cbor:"id"`
	OK bool           `cbor:"ok"`
	E  string         `cbor:"e,omitempty"`
	R  map[string]any `cbor:"r,omitempty"`
}

// Error reports a protocol error; the sender closes the connection after it.
type Error struct {
	T string `cbor:"t"`
	C string `cbor:"c"`
	M string `cbor:"m"`
}

func (Hello) Type() string   { return TypeHello }
func (Welcome) Type() string { return TypeWelcome }
func (Event) Type() string   { return TypeEvent }
func (Ack) Type() string     { return TypeAck }
func (Command) Type() string { return TypeCommand }
func (Result) Type() string  { return TypeResult }
func (Error) Type() string   { return TypeError }

// Error makes an err message usable as a Go error.
func (e Error) Error() string {
	return e.C + ": " + e.M
}

// An EventID is the 16 byte UUID of an event, sent as a CBOR byte string. A
// byte string of any other length is refused instead of padded or cut.
type EventID [16]byte

// NewEventID draws a version 4 UUID from crypto/rand in the RFC 9562 layout.
func NewEventID() (EventID, error) {
	var id EventID
	if _, err := rand.Read(id[:]); err != nil {
		return EventID{}, fmt.Errorf("new event id: %w", err)
	}
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	return id, nil
}

// IsZero reports the empty id, which is never valid.
func (id EventID) IsZero() bool {
	return id == EventID{}
}

// MarshalCBOR writes the id as a byte string.
func (id EventID) MarshalCBOR() ([]byte, error) {
	return encMode.Marshal(id[:])
}

// UnmarshalCBOR accepts exactly 16 bytes.
func (id *EventID) UnmarshalCBOR(data []byte) error {
	var raw []byte
	if err := decMode.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("event id: %w", err)
	}
	if len(raw) != len(id) {
		return fmt.Errorf("event id is %d bytes, want %d", len(raw), len(id))
	}
	copy(id[:], raw)
	return nil
}

// Kind specific payloads, the d map of an ev message.
type (
	// ShotData is the d map of a shot and of a miss.
	ShotData struct {
		Ctl  string `cbor:"ctl"`
		Cseq uint64 `cbor:"cseq"`
	}

	// HitData is the d map of a hit. X and Y are normalized to the target
	// face with the origin top left.
	HitData struct {
		Ctl  string  `cbor:"ctl"`
		Cseq uint64  `cbor:"cseq"`
		X    float64 `cbor:"x"`
		Y    float64 `cbor:"y"`
		Zone string  `cbor:"zone"`
		Pts  int64   `cbor:"pts"`
	}

	// StateData is the d map of a state change.
	StateData struct {
		Scn string `cbor:"scn"`
		St  string `cbor:"st"`
	}

	// HealthData is the d map of the periodic health report. Scn lists the
	// scenario versions the device holds (protocol section 8.10); a nil list
	// is left out and says nothing, an empty one says the device holds none.
	HealthData struct {
		Up   uint64    `cbor:"up"`
		RSSI int64     `cbor:"rssi"`
		Temp float64   `cbor:"temp"`
		Free uint64    `cbor:"free"`
		Scn  []Holding `cbor:"scn,omitzero"`
	}

	// Holding is one scenario version a device holds.
	Holding struct {
		ID  string `cbor:"id"`
		Ver uint64 `cbor:"ver"`
	}

	// ContentData is the d map of a content event: the state St of the
	// scenario version ID and Ver on the device. E says why an install
	// failed.
	ContentData struct {
		ID  string `cbor:"id"`
		Ver uint64 `cbor:"ver"`
		St  string `cbor:"st"`
		E   string `cbor:"e,omitempty"`
	}
)

// ContentAvailable is the argument map of content_available: the scenario
// version ID and Ver, the manifest hash Sha in lower case hex, and the size
// of the package in bytes.
type ContentAvailable struct {
	ID   string `cbor:"id"`
	Ver  uint64 `cbor:"ver"`
	Sha  string `cbor:"sha"`
	Size uint64 `cbor:"size"`
}

// Args returns the announcement as the a map of a command.
func (c ContentAvailable) Args() map[string]any {
	return map[string]any{"id": c.ID, "ver": c.Ver, "sha": c.Sha, "size": c.Size}
}

// DecodeArgs reads the a map of a command into a type such as
// ContentAvailable.
func DecodeArgs(args map[string]any, into any) error {
	raw, err := encMode.Marshal(args)
	if err != nil {
		return fmt.Errorf("%w: command arguments: %v", ErrBadMessage, err)
	}
	if err := decMode.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("%w: command arguments: %v", ErrBadMessage, err)
	}
	return nil
}

var (
	encMode cbor.EncMode
	decMode cbor.DecMode
)

func init() {
	var err error
	encMode, err = cbor.CoreDetEncOptions().EncMode()
	if err != nil {
		panic(err)
	}
	decMode, err = cbor.DecOptions{
		DupMapKey:       cbor.DupMapKeyEnforcedAPF,
		MaxNestedLevels: 16,
		DefaultMapType:  reflect.TypeOf(map[string]any(nil)),
	}.DecMode()
	if err != nil {
		panic(err)
	}
}

// Encode writes a message as one frame, with t set from its type.
func Encode(msg Message) ([]byte, error) {
	var v any
	switch m := msg.(type) {
	case Hello:
		m.T = TypeHello
		v = m
	case Welcome:
		m.T = TypeWelcome
		v = m
	case Event:
		m.T = TypeEvent
		if len(m.D) == 0 {
			m.D = emptyMap
		}
		v = m
	case Ack:
		m.T = TypeAck
		v = m
	case Command:
		m.T = TypeCommand
		if m.A == nil {
			m.A = map[string]any{}
		}
		v = m
	case Result:
		m.T = TypeResult
		v = m
	case Error:
		m.T = TypeError
		v = m
	default:
		return nil, fmt.Errorf("encode %T: %w", msg, ErrUnknownType)
	}
	frame, err := encMode.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", msg.Type(), err)
	}
	if len(frame) > MaxFrameSize {
		return nil, fmt.Errorf("encode %s: %d bytes: %w", msg.Type(), len(frame), ErrFrameTooLarge)
	}
	return frame, nil
}

// EncodeData writes a kind specific payload for the d key of an event.
func EncodeData(data any) (cbor.RawMessage, error) {
	raw, err := encMode.Marshal(data)
	if err != nil {
		return nil, err
	}
	if !isMap(raw) {
		return nil, fmt.Errorf("event data %T is not a CBOR map", data)
	}
	return raw, nil
}

// DecodeData reads the d map of an event into a payload type.
func DecodeData(raw cbor.RawMessage, into any) error {
	if err := decMode.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("%w: event data: %v", ErrBadMessage, err)
	}
	return nil
}

// ControllerID returns the ctl entry of an event payload, if it has a text
// one. Shot, hit and miss carry it; state and health do not.
func ControllerID(raw cbor.RawMessage) (string, bool) {
	var probe struct {
		Ctl *string `cbor:"ctl"`
	}
	if err := decMode.Unmarshal(raw, &probe); err != nil || probe.Ctl == nil {
		return "", false
	}
	return *probe.Ctl, true
}

var emptyMap = cbor.RawMessage{0xa0}

// Decode reads one frame. It returns ErrFrameTooLarge above MaxFrameSize,
// ErrUnknownType for a t it does not know, and ErrBadMessage for anything
// else that is not a well formed message of this version.
func Decode(frame []byte) (Message, error) {
	if len(frame) > MaxFrameSize {
		return nil, fmt.Errorf("decode: %d bytes: %w", len(frame), ErrFrameTooLarge)
	}
	if !isMap(frame) {
		return nil, fmt.Errorf("%w: a frame must be a CBOR map", ErrBadMessage)
	}

	var head struct {
		T string `cbor:"t"`
	}
	if err := decMode.Unmarshal(frame, &head); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadMessage, err)
	}

	var msg Message
	var err error
	switch head.T {
	case "":
		return nil, fmt.Errorf("%w: no message type", ErrBadMessage)
	case TypeHello:
		var m Hello
		err = decMode.Unmarshal(frame, &m)
		msg = m
	case TypeWelcome:
		var m Welcome
		err = decMode.Unmarshal(frame, &m)
		msg = m
	case TypeEvent:
		var m Event
		err = decMode.Unmarshal(frame, &m)
		if err == nil {
			err = validateEvent(m)
		}
		msg = m
	case TypeAck:
		var m Ack
		err = decMode.Unmarshal(frame, &m)
		msg = m
	case TypeCommand:
		var m Command
		err = decMode.Unmarshal(frame, &m)
		msg = m
	case TypeResult:
		var m Result
		err = decMode.Unmarshal(frame, &m)
		msg = m
	case TypeError:
		var m Error
		err = decMode.Unmarshal(frame, &m)
		msg = m
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownType, head.T)
	}
	if err != nil {
		if errors.Is(err, ErrBadMessage) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %s: %v", ErrBadMessage, head.T, err)
	}
	return msg, nil
}

func validateEvent(m Event) error {
	switch {
	case m.ID.IsZero():
		return fmt.Errorf("%w: ev without id", ErrBadMessage)
	case m.Seq == 0:
		return fmt.Errorf("%w: ev with seq 0, sequence numbers start at 1", ErrBadMessage)
	case m.K == "":
		return fmt.Errorf("%w: ev without kind", ErrBadMessage)
	case !isMap(m.D):
		return fmt.Errorf("%w: ev whose d is not a map", ErrBadMessage)
	}
	return nil
}

// isMap reports whether the first data item is a CBOR map, major type 5.
func isMap(raw []byte) bool {
	return len(raw) > 0 && raw[0]>>5 == 5
}
