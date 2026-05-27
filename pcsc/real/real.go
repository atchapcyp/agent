// Real PC/SC adapter — reads Thai ID cards via USB (ebfe/scard) and/or Bluetooth (btbridge).
// Both transports run concurrently and write to the same event channel.
// Auto-detection: USB polls until a reader appears; BT is skipped if binary not found.
//
// Env vars:
//   BTBRIDGE_PATH  path to btbridge binary (default: ./btbridge beside the executable)
package real

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ebfe/scard"
	"github.com/ntl/thai-id-card-reader/agent/pcsc"
	"golang.org/x/text/encoding/charmap"
)

// RealReader is the production adapter — runs USB and BT concurrently.
type RealReader struct{}

// New returns a Reader backed by USB PC/SC and/or Bluetooth.
func New() pcsc.Reader { return &RealReader{} }

// Watch starts both USB and Bluetooth monitors.
// It blocks until both transports exit (which is normally never).
func (r *RealReader) Watch(ch chan<- pcsc.Event) error {
	done := make(chan error, 2)

	go func() { done <- watchUSB(ch) }()
	go func() { done <- watchBluetooth(ch) }()

	// Log errors from either transport; return only when both exit.
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			log.Printf("[pcsc/real] transport exited: %v", err)
		}
	}
	return fmt.Errorf("all PC/SC transports exited")
}

// ── USB transport ─────────────────────────────────────────────────────────────

// APDU commands (Thai ID card — TNI standard)
var (
	cmdSelectAID = []byte{0x00, 0xA4, 0x04, 0x00, 0x08, 0xA0, 0x00, 0x00, 0x00, 0x54, 0x48, 0x00, 0x01}
	cmdCID       = []byte{0x80, 0xB0, 0x00, 0x04, 0x02, 0x00, 0x0D} // 13 bytes, ASCII
	cmdNameTH    = []byte{0x80, 0xB0, 0x00, 0x11, 0x02, 0x00, 0x64} // 100 bytes, TIS-620
	cmdNameEN    = []byte{0x80, 0xB0, 0x00, 0x75, 0x02, 0x00, 0x64} // 100 bytes, ASCII
	cmdDOB       = []byte{0x80, 0xB0, 0x00, 0xD9, 0x02, 0x00, 0x08} // 8 bytes, ASCII
	cmdAddress   = []byte{0x80, 0xB0, 0x15, 0x79, 0x02, 0x00, 0x64} // 100 bytes, TIS-620
)

func watchUSB(ch chan<- pcsc.Event) error {
	ctx, err := scard.EstablishContext()
	if err != nil {
		return fmt.Errorf("USB: establish context: %w", err)
	}
	defer ctx.Release()

	readerName, err := waitForUSBReader(ctx)
	if err != nil {
		return err
	}
	log.Printf("[pcsc/usb] using reader: %s", readerName)

	states := []scard.ReaderState{{
		Reader:       readerName,
		CurrentState: scard.StateUnaware,
	}}
	wasPresent := false

	for {
		if err := ctx.GetStatusChange(states, -1); err != nil {
			return fmt.Errorf("USB: GetStatusChange: %w", err)
		}

		eventState := states[0].EventState
		isPresent := eventState&scard.StatePresent != 0

		if isPresent && !wasPresent {
			log.Println("[pcsc/usb] card INSERTED — reading…")
			data, err := readCardUSB(ctx, readerName)
			if err != nil {
				log.Printf("[pcsc/usb] read error: %v", err)
			} else {
				ch <- pcsc.Event{Type: "card_inserted", Data: data}
			}
		}
		if !isPresent && wasPresent {
			log.Println("[pcsc/usb] card REMOVED")
			ch <- pcsc.Event{Type: "card_removed"}
		}

		wasPresent = isPresent
		states[0].CurrentState = eventState
	}
}

func waitForUSBReader(ctx *scard.Context) (string, error) {
	for {
		readers, err := ctx.ListReaders()
		if err == nil && len(readers) > 0 {
			return readers[0], nil
		}
		log.Println("[pcsc/usb] no reader found, retrying in 2s…")
		time.Sleep(2 * time.Second)
	}
}

func readCardUSB(ctx *scard.Context, readerName string) (*pcsc.CardData, error) {
	card, err := ctx.Connect(readerName, scard.ShareShared, scard.ProtocolT1)
	if err != nil {
		card, err = ctx.Connect(readerName, scard.ShareShared, scard.ProtocolT0)
		if err != nil {
			return nil, fmt.Errorf("connect: %w", err)
		}
	}
	defer card.Disconnect(scard.LeaveCard)

	if _, err := execAPDU(card, cmdSelectAID); err != nil {
		return nil, fmt.Errorf("SELECT AID: %w", err)
	}
	cidBytes, err := execAPDU(card, cmdCID)
	if err != nil {
		return nil, fmt.Errorf("READ CID: %w", err)
	}
	nameTHBytes, err := execAPDU(card, cmdNameTH)
	if err != nil {
		return nil, fmt.Errorf("READ NameTH: %w", err)
	}
	nameENBytes, err := execAPDU(card, cmdNameEN)
	if err != nil {
		return nil, fmt.Errorf("READ NameEN: %w", err)
	}
	dobBytes, err := execAPDU(card, cmdDOB)
	if err != nil {
		return nil, fmt.Errorf("READ DOB: %w", err)
	}
	addressBytes, err := execAPDU(card, cmdAddress)
	if err != nil {
		return nil, fmt.Errorf("READ Address: %w", err)
	}

	return &pcsc.CardData{
		CID:     trimASCII(cidBytes),
		NameTH:  decodeTIS620(nameTHBytes),
		NameEN:  trimASCII(nameENBytes),
		DOB:     trimASCII(dobBytes),
		Address: decodeTIS620(addressBytes),
	}, nil
}

// execAPDU transmits an APDU and handles T=0 status words (0x61/0x6C).
func execAPDU(card *scard.Card, cmd []byte) ([]byte, error) {
	resp, err := card.Transmit(cmd)
	if err != nil {
		return nil, fmt.Errorf("transmit: %w", err)
	}
	for {
		if len(resp) < 2 {
			return nil, fmt.Errorf("response too short: %X", resp)
		}
		sw1, sw2 := resp[len(resp)-2], resp[len(resp)-1]
		switch {
		case sw1 == 0x90 && sw2 == 0x00:
			return resp[:len(resp)-2], nil
		case sw1 == 0x61:
			resp, err = card.Transmit([]byte{0x00, 0xC0, 0x00, 0x00, sw2})
			if err != nil {
				return nil, fmt.Errorf("GET RESPONSE: %w", err)
			}
		case sw1 == 0x6C:
			newCmd := make([]byte, len(cmd))
			copy(newCmd, cmd)
			newCmd[len(newCmd)-1] = sw2
			resp, err = card.Transmit(newCmd)
			if err != nil {
				return nil, fmt.Errorf("resend Le=%02X: %w", sw2, err)
			}
		default:
			return nil, fmt.Errorf("APDU SW=%02X%02X", sw1, sw2)
		}
	}
}

// ── Bluetooth transport ───────────────────────────────────────────────────────

// btEvent mirrors the JSON lines emitted by the btbridge subprocess.
type btEvent struct {
	Type  string          `json:"type"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error string          `json:"error,omitempty"`
}

// btCardData matches btbridge's snake_case card_data payload.
type btCardData struct {
	IDNumber    string `json:"id_number"`
	NameTH      string `json:"name_th"`
	NameEN      string `json:"name_en"`
	DateOfBirth string `json:"date_of_birth"`
	Address     string `json:"address"`
}

func watchBluetooth(ch chan<- pcsc.Event) error {
	bridgePath := btbridgePath()
	if bridgePath == "" {
		log.Println("[pcsc/bt] btbridge binary not found — Bluetooth disabled")
		return nil // not fatal — USB still runs
	}

	log.Printf("[pcsc/bt] starting btbridge: %s", bridgePath)

	for {
		if err := runBTBridge(bridgePath, ch); err != nil {
			log.Printf("[pcsc/bt] btbridge exited: %v — restarting in 5s", err)
		}
		time.Sleep(5 * time.Second)
	}
}

func runBTBridge(bridgePath string, ch chan<- pcsc.Event) error {
	cmd := exec.Command(bridgePath)
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start: %w", err)
	}
	log.Printf("[pcsc/bt] btbridge started (pid=%d)", cmd.Process.Pid)

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var ev btEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			log.Printf("[pcsc/bt] bad JSON: %v | %s", err, line)
			continue
		}
		handleBTEvent(ev, ch)
	}

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("process: %w", err)
	}
	return nil
}

func handleBTEvent(ev btEvent, ch chan<- pcsc.Event) {
	switch ev.Type {
	case "card_data":
		var d btCardData
		if err := json.Unmarshal(ev.Data, &d); err != nil {
			log.Printf("[pcsc/bt] bad card_data payload: %v", err)
			return
		}
		ch <- pcsc.Event{
			Type: "card_inserted",
			Data: &pcsc.CardData{
				CID:     d.IDNumber,
				NameTH:  d.NameTH,
				NameEN:  d.NameEN,
				DOB:     d.DateOfBirth,
				Address: d.Address,
			},
		}
	case "card_removed":
		ch <- pcsc.Event{Type: "card_removed"}
	case "status":
		// informational only — log but don't emit to browser
		log.Printf("[pcsc/bt] status: %s", ev.Data)
	case "error":
		log.Printf("[pcsc/bt] error from bridge: %s", ev.Error)
	}
}

// btbridgePath returns the path to the btbridge binary, or empty string if not found.
// Search order: BTBRIDGE_PATH env var → beside executable → ./btbridge (cwd).
func btbridgePath() string {
	if p := os.Getenv("BTBRIDGE_PATH"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
		log.Printf("[pcsc/bt] BTBRIDGE_PATH=%s not found", p)
		return ""
	}

	// Look beside the running executable
	exe, err := os.Executable()
	if err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "btbridge")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	// Fall back to cwd
	if _, err := os.Stat("./btbridge"); err == nil {
		return "./btbridge"
	}

	return ""
}

// ── Encoding helpers ──────────────────────────────────────────────────────────

func decodeTIS620(b []byte) string {
	b = bytes.TrimRight(b, "\x00 ")
	decoded, err := charmap.Windows874.NewDecoder().Bytes(b)
	if err != nil {
		return strings.TrimRight(string(b), "\x00 ")
	}
	return strings.TrimSpace(string(decoded))
}

func trimASCII(b []byte) string {
	return strings.TrimRight(strings.TrimRight(string(b), "\x00"), " ")
}
