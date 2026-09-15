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
