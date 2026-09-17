# theserver device link protocol, version 1

Status: draft for Season S01, written before the simulated target exists. This document only grows; superseded statements are marked with a date.

## 1. Transport

One outbound WebSocket connection per device, opened by the device, kept open for its lifetime.

- URL: `wss://<server>:<port>/link/v1`
- Messages: binary WebSocket frames, each frame one CBOR map.
- Keepalive: WebSocket ping from the server every 15 seconds, pong expected within 10 seconds; otherwise the server closes the connection and marks the device offline.
- S01 authentication: HTTP header `Authorization: Bearer <device_token>` on the upgrade request. Tokens are created by the operator (admin UI or API) per device and stored hashed. Mutual TLS with device certificates replaces this in Season S02; the message schema does not change.

## 2. Envelope

Every message is a CBOR map with a text key `t` (type). Keys are short ASCII strings to keep frames small on an ESP32.

| `t` | Direction | Purpose |
| --- | --- | --- |
| `hello` | device to server | first message after connect |
| `welcome` | server to device | reply to hello, carries ack and server time |
| `ev` | device to server | one event |
| `ack` | server to device | cumulative acknowledgement |
| `cmd` | server to device | command |
| `res` | device to server | command result |
| `err` | either | protocol error, followed by close |

Unknown keys are ignored. Unknown `t` values produce `err` with code `unknown_type`.

## 3. Handshake and replay

`hello`: `{t:"hello", dev:<device_id>, fw:<firmware version>, cls:<"esp"|"pi"|"pc">, last:<last seq the device has journaled>, proto:1}`

`welcome`: `{t:"welcome", ack:<last seq the server has stored for this device>, now:<server unix time in ms>, ses:<session id or null>}`

After `welcome` the device sends every journaled event with `seq > ack`, in order, then continues live. The server stores each event idempotently on `id`; a duplicate is acknowledged but not stored twice. Sequence numbers are per device, start at 1, never reused, and survive device reboots (the device persists its counter).

If `last` is smaller than `ack` the server answers `err` with code `seq_regression`; the operator must resolve this in the admin UI (device reset).

## 4. Events

`ev`: `{t:"ev", id:<event id, 16 byte UUID as byte string>, seq:<uint64>, k:<kind>, ts:<device unix time in ms>, d:<kind specific map>}`

Kinds in S01:

| `k` | `d` | Meaning |
| --- | --- | --- |
| `shot` | `{ctl:<controller id>, cseq:<controller shot number>}` | a shot packet was heard over ESP-NOW |
| `hit` | `{ctl, cseq, x:<0..1>, y:<0..1>, zone:<text>, pts:<int>}` | the target registered a hit |
| `miss` | `{ctl, cseq}` | shot heard, no hit registered |
| `state` | `{scn:<scenario id>, st:<text>}` | target state changed |
| `health` | `{up:<seconds>, rssi:<int>, temp:<float>, free:<bytes>}` | periodic health |

`x` and `y` are normalized to the target face, origin top left. `zone` is the target's own classification (`head`, `torso`, `arm`, `leg`, `none`). `pts` is the points the target awarded under its loaded rules; the server may recompute.

`ack`: `{t:"ack", seq:<highest contiguous seq stored>}`. Sent at most every 100 ms or every 32 events, whichever comes first, and immediately after replay completes.

## 5. Commands

`cmd`: `{t:"cmd", id:<command id>, n:<name>, a:<args map>}`
`res`: `{t:"res", id:<command id>, ok:<bool>, e:<error text or absent>, r:<result map or absent>}`

Names in S01: `time_mark {now}`, `session_start {ses, scn}`, `session_stop {ses}`, `set_config {k, v}`, `reboot {}`. Every command must be answered with `res` within 5 seconds or the server records a timeout.

## 6. Errors

`err`: `{t:"err", c:<code>, m:<message>}`. Codes: `unknown_type`, `bad_message`, `seq_regression`, `unauthorized`, `proto_unsupported`.

## 7. Limits

Frame size at most 4096 bytes. Payloads that do not fit are a design error, not a runtime case. Large content never uses this link; it is fetched over HTTPS.

## 8. Server behaviour

Added 16 September 2026 with S01-B03. This section states what theserver does, as implemented in `internal/link`; sections 1 to 7 are unchanged. The numbers in brackets are the configuration keys and their defaults.

### 8.1 Before the upgrade

- The server hashes the bearer token with SHA-256 and looks up the device holding that hash. The scheme `Bearer` is matched without regard to case.
- A missing or malformed header, an unknown token, or a device whose status is not `approved` is answered with HTTP 401, `WWW-Authenticate: Bearer realm="theserver device link"`, `Content-Type: application/cbor` and an `err` message with code `unauthorized` as the body. No upgrade happens.
- If the device registry cannot be read, or the server is shutting down, the answer is HTTP 503.
- The server reads frames of at most 4096 bytes. A larger frame closes the connection with WebSocket status 1009.

### 8.2 Handshake

- The device has 5 seconds (`link.hello_timeout_s`, 5) for `hello`. Without it the connection is closed without a close frame.
- A first frame that is not `hello` gets `bad_message`. `proto` other than 1 gets `proto_unsupported`. A `dev` other than the device of the token gets `unauthorized`. A `cls` other than `esp`, `pi` or `pc` gets `bad_message`. A known `cls` that differs from the registered class is logged and accepted.
- `fw` is recorded as the firmware version of the device when it differs from the stored one.
- A device has at most one connection. When a new connection passes these checks while an older one is open, the older one is closed without a close handshake, its received events are stored, and only then does the handshake of the new one go on. The server waits at most 10 seconds for the older connection.
- The server reads its `ack`: the highest seq such that every seq from 1 to it is stored. If `hello.last` is below it, the answer is `seq_regression`, naming both numbers, and the close.
- `welcome` carries that `ack`, `now` from the server clock, and `ses`: the running session that holds the device, the latest started if several do, or null.
- If `hello.last` is not above `ack`, there is nothing to replay; the replay is complete at once and an `ack` with the same seq follows `welcome` immediately.

### 8.3 Events and acknowledgements

- Received events are stored in batches. A batch is written when its first event is 100 ms old (`link.ack_interval_ms`, 100) or when it holds 32 events (`link.ack_batch`, 32), whichever comes first. Each batch is one transaction.
- Every stored batch is followed by an `ack` with the contiguous seq. When a seq is missing, the ack stays below it, even if later events are stored, until the gap is filled.
- The replay is complete when the ack reaches `hello.last`. The server logs it with the number of replayed events and duplicates.
- An event whose `id` is already stored for the same device and seq is a duplicate: it is not stored again, and the ack covers it as usual.
- `ctl` in `d` becomes the controller of the event when it is text. `d` is stored byte for byte as it arrived. `ts_server` comes from the server clock.
- An event is attributed to the session of its device whose start and end cover its `ts`, start inclusive and end exclusive, the latest started if several do; a running session has no end yet. Replayed events therefore land in the session they happened in, as long as the device clock follows `time_mark`.
- An `ev` without a 16 byte `id`, with `seq` 0, without `k`, or with a `d` that is not a map gets `bad_message`. A `k` the server does not know is stored like any other.
- A conflict gets `bad_message` with the seq in the message: a seq that another event id already holds, or an `id` already stored under another device or seq. The whole batch that contained it is not stored, events received after it on the same connection are dropped, and the connection is closed. Nothing of that batch is acknowledged, so the device still holds it and replays it after reconnecting; the conflicting event keeps failing until the operator resets the device.
- If the journal cannot be written, the connection is closed with WebSocket status 1011 and no `err`; the device replays what was not acknowledged.
- The last seen time of the device is updated on every frame it sends.

### 8.4 Other frames from a device

- `res` is matched to a waiting command by `id`; a `res` for an unknown id is logged and ignored.
- `err` from a device is logged, and the connection is closed with status 1000.
- `hello` after the handshake, `welcome`, `ack` or `cmd` from a device get `bad_message`.
- An unknown `t` gets `unknown_type`. A text frame, a frame that is not a CBOR map, and a map with a repeated key get `bad_message`.
- After an `err` the server sends nothing more and closes the connection with status 1008.

### 8.5 Commands

- The command `id` is an unsigned integer, counted per connection from 1. `a` is always a map, empty when a command has no arguments.
- A `res` has to arrive within 5 seconds. Otherwise the server logs a timeout and the caller gets a timeout error.
- Commands still waiting when a connection ends fail as device offline.

### 8.6 Keepalive and write timeout

- The server sends a WebSocket ping every 15 seconds (`link.ping_interval_s`, 15). Without a pong within 10 seconds (`link.pong_timeout_s`, 10) the connection is closed without a close frame and the device is offline.
- A frame the device does not take within 5 seconds closes the connection.

### 8.7 Revocation and shutdown

- Every 500 ms the server looks for connected devices whose status is no longer `approved`. Such a connection gets `unauthorized` and is closed with status 1008; a new attempt gets HTTP 401.
- On shutdown the server refuses new connections with HTTP 503, stores what every connection has received, and closes each with status 1001.

### 8.8 Sequence epochs and device reset

Added 17 September 2026 with S01-B04 (D-026). It extends 8.2 and 8.3; what they say about seq, ack and `seq_regression` holds within one epoch.

- Every device has a sequence epoch, a counter that starts at 1. Events are stored with the epoch they arrived in; a seq is unique per device and epoch, and the `ack` of 8.2 and 8.3 counts only the events of the current epoch.
- An operator reset moves the device to the next epoch. It starts at seq 1 again, and the events of earlier epochs stay in the journal.
- `welcome` carries the current epoch under the key `ep`, an unsigned integer: `{t:"welcome", ack, now, ses, ep}`. A device that does not know `ep` ignores it, as section 2 asks. The `ack` in the same `welcome` belongs to that epoch; after a reset it is 0 until the device sends again.
- A device that stores the epoch compares `ep` with the epoch of its journal. When they differ, the device drops the events it still holds unacknowledged, sets its counter so that its next event has seq 1, stores the new epoch, closes the connection and connects again at once. The `last` of its next `hello` then belongs to the new epoch. The reference is simtarget; firmware follows it in a later season.
- When a device is reset while it is connected, the server closes the connection with WebSocket status 1012 and no `err`. Events it had received and not yet stored are dropped with the connection; they belong to the old epoch.
- A connection stores events only into the epoch it read at its handshake. If the epoch changes while a batch is being stored, that batch is not stored and the connection is closed with status 1012 as above.
- A device without an epoch in its journal is in epoch 1.

### 8.9 Token replacement and the status check

Added 17 September 2026 with S01-B04 (D-026). It extends 8.7.

- An operator can give a device a new token through the API or the admin page. The new token is shown once; only its SHA-256 is stored, and the old token stops working at once: a new attempt with it gets HTTP 401 as in 8.1.
- A device connected with the old token gets `err` with code `unauthorized` and the message that the token was replaced, and its connection is closed with status 1008. Events it had received and not yet stored are dropped; the device still holds them and sends them again once it connects with the new token.
- The check every 500 ms of 8.7 now compares three things for every connected device: its status, the hash of its token and its epoch. A status other than `approved` closes as in 8.7, a different token hash as in this section, and a different epoch as in 8.8. This also covers changes made by another process, for example `theserver device reset` beside the running server.
- Operator actions through the API or the admin page close the connection at once, without waiting for the check.
