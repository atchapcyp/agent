# Thai ID Card Reader — Agent

Desktop agent that reads Thai National ID cards and streams data to browsers via WebRTC DataChannel.

## Overview

```
USB/Bluetooth Card Reader
        │
   [Agent (Go)]  ──── WebRTC P2P ────▶  Browser
        │
   Signaling Server (WebSocket)
```

- Reads card data using ISO 7816 APDU commands (TNI standard)
- Streams data securely via WebRTC DataChannel (DTLS encrypted)
- Supports USB PC/SC readers and Bluetooth readers (via btbridge)
- Mock mode for development without hardware

## Requirements

- Go 1.21+
- macOS: Xcode CLI tools (`xcode-select --install`)
- Linux: `libpcsclite-dev` + `pcscd`
- Windows: WinSCard (built-in)
    - Driver for Feitain bR301 (Bluetooth case at bR301 Bluetooth Driver folder > bR301Driver.exe): https://github.com/FeitianSmartcardReader/bR301_SDK_Latest 

## Quick Start

```bash
# Mock mode — no card reader needed (press Enter to simulate insert/remove)
make run-mock

# Real USB PC/SC reader
make run

# Real USB + Bluetooth
make run-bt
```

## Make Targets

| Target | Description |
|--------|-------------|
| `make build` | Compile binary |
| `make run` | Build + run with real USB reader |
| `make run-mock` | Build + run with stdin mock |
| `make run-bt` | Build + run with USB + Bluetooth |
| `make clean` | Remove compiled binary |

Override defaults inline:
```bash
make run ROOM=my-room SIGNAL=wss://your-signal-server/ws
```

## Configuration

| Env Var | Default | Description |
|---------|---------|-------------|
| `SIGNAL_URL` | `wss://signal-production-b59d.up.railway.app/ws` | Signaling server WebSocket URL |
| `ROOM_ID` | `demo-room-1` | Room ID (must match browser) |
| `PCSC_MOCK` | `0` | Set `1` to use stdin mock instead of real hardware |
| `BTBRIDGE_PATH` | auto-detect | Path to `btbridge` binary for Bluetooth support |

## Adapter Selection

The agent uses a **ports and adapters** pattern for card readers:

```
pcsc/
├── reader.go       # Reader interface (port)
├── mock.go         # MockReader — stdin-driven (press Enter)
└── real/
    └── real.go     # RealReader — USB PC/SC + Bluetooth
```

| Mode | Adapter | How |
|------|---------|-----|
| `PCSC_MOCK=1` | `MockReader` | Press Enter = card insert, Enter again = remove |
| default | `RealReader` | USB via `ebfe/scard` + optional BT via `btbridge` |

## Bluetooth Support

Bluetooth uses a compiled Swift bridge binary (`btbridge`) that communicates with iOS/macOS Bluetooth RFCOMM readers.

## Card Data Fields

Fields read from card via APDU:

| Field | Description | Encoding |
|-------|-------------|----------|
| `cid` | 13-digit national ID number | ASCII |
| `nameTH` | Full name (Thai) | TIS-620 |
| `nameEN` | Full name (English) | ASCII |
| `dob` | Date of birth `YYYYMMDD` | ASCII |
| `address` | Address | TIS-620 |

## WebRTC DataChannel Messages

**Agent → Browser:**
```json
{ "event": "card_inserted", "data": { "cid": "...", "nameTH": "...", ... } }
{ "event": "card_removed" }
{ "event": "reader_status", "status": "connected|disconnected|error" }
{ "event": "photo_response", "data": { "photoBase64": "..." } }
```

**Browser → Agent:**
```json
{ "event": "request_photo" }
```

## Project Structure

```
agent/
├── main.go              # Entry point — wires adapters + signaling + WebRTC
├── Makefile
├── pcsc/
│   ├── reader.go        # Reader port interface + CardData + Event types
│   ├── mock.go          # Mock adapter (stdin)
│   └── real/
│       └── real.go      # Real adapter (USB PC/SC + Bluetooth)
├── signaling/
│   └── client.go        # WebSocket client for signaling server
└── webrtc/
    └── manager.go       # RTCPeerConnection manager (one per browser tab)
```

## Related Projects

- [`../signal`](../signal) — Signaling server (Go, deployable to Railway/Fly.io)
- [`../web-demo`](../web-demo) — Browser demo page (vanilla HTML/JS)
