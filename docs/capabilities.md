# theserver capability map

Status: reference, version 1, 16 September 2026. This document only grows. It lists what theserver is meant to do, grouped by area, with the phase in which each capability is expected. Phases are not seasons; the founder decides when a season starts and ends, and a phase may span several seasons.

Phases: **Foundation** (the core that everything else sits on), **First Venue** (what the founder's own cinema needs on opening night), **Growth** (what makes people come back and lets operators run bigger), **Federation** (several venues, the manufacturer's network, FIND A CINEMA).

## 0. Ground rules that shape every capability

- The hit reacts locally; theserver is never in the hit path.
- theserver never touches money. It records what was played and sold, computes, reports and exports. It is not a cash register and never becomes one.
- Everything is configurable, every setting has a documented default, and every setting is reachable from the admin interface and the API.
- The admin interface is a product surface. Nothing in it exists without a working backend behind it.
- CIND3R3LLA is the assistant across the whole system. Every capability below is also reachable for her through the API, with permissions in three tiers: read, configure, control. She never gets firing authority on a hand held device.
- Security is built in: passkeys for people, certificates for devices, signed content, signed updates, an audit log for every change.

## 1. Foundation

| Capability | Notes |
| --- | --- |
| Device link | WebSocket over TLS, CBOR, idempotent journal, replay after reconnect (docs/protocol.md) |
| Event journal | Immutable, per device sequence, contiguous ack, never lost, never doubled |
| Device registry | Kind, class (esp, pi, pc), room, zone, status, firmware, config |
| Sessions | Scenario, room, devices, players, state, timestamps |
| Rankings from events | Computed, never edited by hand |
| Configuration | TOML file, environment, flags, admin UI; defaults written out with comments |
| API v1 | JSON over HTTPS, token authenticated, OpenAPI served by the binary |
| First admin page | Devices, sessions, ranking, settings |
| One binary per platform | Linux x86-64, Linux ARM64, Windows; first start creates data dir, config, database, self signed certificate |

## 2. First Venue

### 2.1 Before the game: booking and arrival

- Online reservation from the website: single players, groups, company events, birthdays, vouchers, waiting list
- Check-in by wristband or owned controller: tap and you are in the line-up
- Entrance display: which room is free when, who is up next
- Online prepayment through the operator's own account at a payment provider (Stripe, Mollie or similar); money goes to the operator, theserver only receives the confirmation

### 2.2 During the game: direction

- Director screen for supervision: every room, every target, every controller live
- Difficulty per room as a slider; scenario switch during operation; disable a target for one group; emergency stop per room
- Spectator screens in the waiting area: live scores, slow motion hit replays, ranking of the evening
- Time base broadcast and beacon multiplex direction (see cyb3rgun-reference section 6.5)

### 2.3 People and access

- Passkeys (WebAuthn) for operator, staff, technician; roles: owner, desk, supervisor, technician
- Audit log of every change with who, when, what
- Member accounts with history; age gate for 18+ content and for controllers with recoil or emitter

### 2.4 Device management

- Enrol by shooting: a supervisor controller marked as enrolment device shoots the new target; it joins the system in the room the controller is in
- Enrol by NFC: hold the operator's phone to the target (PN532 module), the device's admin page opens
- QR code and self test on the target display at boot; beacons blink in sequence and a controller in the room confirms it sees four points
- 2D floor plan with live status per device: signal, last seen, hits in the last minute as a heat map; silent devices turn yellow, then red
- Firmware and content rollout in stages (one device, one room, all), signed, with checksum and rollback
- Backup with one click, restore on new hardware in minutes

### 2.5 Money: the numbers

- Price lists per venue, per room, per scenario, with validity dates
- Revenue per room, per hour, per scenario; occupancy; revenue per guest
- Export as CSV and DATEV format; everything also through the API
- Point of sale integration in three stages, never as a cash register: (1) deliver the numbers for the operator's own register; (2) connect common POS systems (SumUp, Zettle, ready2order, orderbird or similar) so a booked session appears as an item and the register reports "paid" back; (3) online prepayment as in 2.1

### 2.6 Franchise accounting

- The base software is free for private and commercial use, forever
- Threshold per month, set by the manufacturer per venue with a validity date; a venue that stays below pays nothing
- Above the threshold, a percentage of system revenue (revenue booked through theserver: sessions, play time, tournaments; not gastronomy, not merchandise). The rate is set by the manufacturer per venue; the working proposal is 7 percent
- theserver computes the amount, shows it to the operator in advance, and sends a signed monthly statement to the manufacturer; the manufacturer issues the invoice. Nothing is debited automatically; theserver has no access to money
- Alerts to the manufacturer when a venue crosses the threshold during the month, plus trend alerts
- Enforcement is by contract and by signed content: after warning, invoice, payment term and reminder, licensed content and updates freeze and the venue leaves the federation map; the free base keeps running. Grace periods are contract terms and are configured, not hard coded

## 3. Growth

### 3.1 The reason to come back

- Member profile: hit rate, reaction time, steadiness from IMU data, best videos, ranking position
- Skill rating in the style of chess ratings so beginners meet beginners
- Badges, seasonal rankings per venue, tournaments with registration and prize money, leagues
- End of evening link to the phone: your statistics, your place, your best clip

### 3.2 Controllers owned by members

- A controller has an owner (fixed, changes on sale) and a current shooter (changes by wristband); a rental controller's owner is the venue
- Owned controllers work in every venue; without federation access they run as guest devices with a local account
- Do it yourself controllers register the same way; the manufacturer sells parts, kits and finished devices

### 3.3 Wristbands and tags

- NFC wristband, card or key fob as player identity: tap the controller grip, every hit is credited to the member until tap-out or session end
- Fingerprint only as an optional module for a member's own device, template stored on the device, never on the server; not for venue rental (gloves, sweat, biometric data obligations)

### 3.4 Content catalogue

- Install scenarios from the federation catalogue with one click; community packages with ratings; upload and release packages from the editor
- Age rating per package, licence check, staged distribution to targets
- Pro content marked as such; free content is free

### 3.5 CIND3R3LLA in the venue

- Briefing over speakers, commentary during play, results announcement
- Technician in the ear of the supervisor: "target 7 is silent, a spare is in storage, shall I enrol it?"
- Match setup by voice, teams, respawn rules, difficulty, environment calibration on command

## 4. Federation

- FIND A CINEMA on the website is the list of licensed venues; a venue that loses its licence leaves the map
- Accounts, owned controllers and wristbands valid in every federated venue
- Federation wide leagues and rankings; venue comparison for the manufacturer
- Franchise statements collected centrally; invoicing from the manufacturer's own accounting system
- Optional hosting by the manufacturer as a paid service; self hosting stays free

## 5. Deliberately out of scope

- Cash handling, receipts, fiscal signature units, any cash register function
- Holding or forwarding money
- Storing identity documents for age verification (handled by the shipping and Pro age check processes)
- Biometric data on the server
