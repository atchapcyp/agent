// Real PC/SC adapter — reads Thai ID cards via USB (ebfe/scard) and/or Bluetooth (btbridge).
// Both transports run concurrently and write to the same event channel.
// Auto-detection: USB polls until a reader appears; BT is skipped if binary not found.
//
// Env vars:
//
//	BTBRIDGE_PATH  path to btbridge binary (default: ./btbridge beside the executable)
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
	"runtime"
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
	done := make(chan error, 1)

	go func() { done <- watchUSB(ch) }()

	// Log errors from either transport; return only when both exit.
	if err := <-done; err != nil {
		log.Printf("[pcsc/real] transport exited: %v", err)
	}

	return fmt.Errorf("all PC/SC transports exited")
}

// ── USB transport ─────────────────────────────────────────────────────────────

// APDU commands (Thai ID card — TNI standard)
// 0x80, 0xb0, 0x00, 0xf6, 0x02, 0x00, 0x64
var (
	cmdSelectAID  = []byte{0x00, 0xA4, 0x04, 0x00, 0x08, 0xA0, 0x00, 0x00, 0x00, 0x54, 0x48, 0x00, 0x01}
	cmdCID        = []byte{0x80, 0xB0, 0x00, 0x04, 0x02, 0x00, 0x0D} // 13 bytes, ASCII
	cmdNameTH     = []byte{0x80, 0xB0, 0x00, 0x11, 0x02, 0x00, 0x64} // 100 bytes, TIS-620
	cmdNameEN     = []byte{0x80, 0xB0, 0x00, 0x75, 0x02, 0x00, 0x64} // 100 bytes, ASCII
	cmdDOB        = []byte{0x80, 0xB0, 0x00, 0xD9, 0x02, 0x00, 0x08} // 8 bytes, ASCII
	cmdAddress    = []byte{0x80, 0xB0, 0x15, 0x79, 0x02, 0x00, 0x64} // 100 bytes, TIS-620
	cmdGender     = []byte{0x80, 0xB0, 0x00, 0xE1, 0x02, 0x00, 0x01} //  bytes, ASCII
	cmdIssueDate  = []byte{0x80, 0xB0, 0x01, 0x67, 0x02, 0x00, 0x08} // bytes, ASCII
	cmdExpireDate = []byte{0x80, 0xB0, 0x01, 0x6F, 0x02, 0x00, 0x08} // bytes, ASCII
	cmdCardIssuer = []byte{0x80, 0xB0, 0x00, 0xF6, 0x02, 0x00, 0x64} // bytes, TIS-620
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
		log.Printf("[pcsc/usb] readers found: %v", readers)

		if err == nil && len(readers) > 0 {
			for _, name := range readers {
				states := []scard.ReaderState{{
					Reader:       name,
					CurrentState: scard.StateUnaware,
				}}

				err := ctx.GetStatusChange(states, 0)
				if err != nil {
					log.Println("got error when get status: ", err.Error())
				}

				for index, state := range states {
					log.Println(name, " state[", index, "] - EventState: ", state.EventState)
					decodeEventState(name, state.EventState)
					// log.Println(name, " CurrentState[", index, "]: ", state.CurrentState)
					// log.Println(name, " EventState[", index, "]: ", state.EventState)
				}
			}

			return readers[0], nil
		}
		log.Println("[pcsc/usb] no reader found, retrying in 2s…")
		time.Sleep(2 * time.Second)
	}
}

func readCardUSB(ctx *scard.Context, readerName string) (*pcsc.CardData, error) {
	// Try ShareExclusive first (recommended for USB readers to prevent CertPropSvc interference).
	card, err := ctx.Connect(readerName, scard.ShareExclusive, scard.ProtocolT0)
	if err != nil {
		card, err = ctx.Connect(readerName, scard.ShareExclusive, scard.ProtocolAny)
		if err != nil {
			// Some virtual drivers (like Bluetooth PC/SC drivers) do not support Exclusive mode.
			// Fall back to ShareShared if ShareExclusive fails.
			log.Println("[pcsc/usb] ShareExclusive failed")
			return nil, fmt.Errorf("connect: %w", err)
		}
	}
	defer card.Disconnect(scard.LeaveCard)

	if _, err := execAPDU(card, cmdSelectAID); err != nil {
		return nil, fmt.Errorf("SELECT AID: %w", err)
	}
	cidBytes, err := execAPDU(card, cmdCID)
	if err != nil {
		fmt.Println(fmt.Errorf("READ CID: %w", err))
	}
	nameTHBytes, err := execAPDU(card, cmdNameTH)
	if err != nil {
		fmt.Println(fmt.Errorf("READ NameTH: %w", err))
	}
	nameENBytes, err := execAPDU(card, cmdNameEN)
	if err != nil {
		fmt.Println(fmt.Errorf("READ NameEN: %w", err))
	}
	dobBytes, err := execAPDU(card, cmdDOB)
	if err != nil {
		fmt.Println(fmt.Errorf("READ DOB: %w", err))
	}
	addressBytes, err := execAPDU(card, cmdAddress)
	if err != nil {
		fmt.Println(fmt.Errorf("READ Address: %w", err))
	}
	genderBytes, err := execAPDU(card, cmdGender)
	if err != nil {
		fmt.Println(fmt.Errorf("READ Gender: %w", err))
	}
	issueDateBytes, err := execAPDU(card, cmdIssueDate)
	if err != nil {
		fmt.Println(fmt.Errorf("READ IssueDate: %w", err))
	}
	expireDateBytes, err := execAPDU(card, cmdExpireDate)
	if err != nil {
		fmt.Println(fmt.Errorf("READ ExpireDate: %w", err))
	}
	cardIssuerBytes, err := execAPDU(card, cmdCardIssuer)
	if err != nil {
		fmt.Println(fmt.Errorf("READ CardIssuer: %w", err))
	}

	return &pcsc.CardData{
		CID:        trimASCII(cidBytes),
		NameTH:     decodeTIS620(nameTHBytes),
		NameEN:     trimASCII(nameENBytes),
		DOB:        trimASCII(dobBytes),
		Address:    decodeTIS620(addressBytes),
		Gender:     trimASCII(genderBytes),
		IssueDate:  trimASCII(issueDateBytes),
		ExpireDate: trimASCII(expireDateBytes),
		CardIssuer: decodeTIS620(cardIssuerBytes),
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
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}

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
		candidate := filepath.Join(filepath.Dir(exe), "btbridge"+ext)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	// Fall back to cwd
	candidateCWD := "./btbridge" + ext
	if _, err := os.Stat(candidateCWD); err == nil {
		return candidateCWD
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

func decodeEventState(name string, eventState scard.StateFlag) {
	flags := uint32(eventState) & 0xFFFF
	counter := uint32(eventState) >> 16

	var active []string
	checks := []struct {
		flag uint32
		name string
	}{
		{uint32(scard.StateIgnore), "Ignore"},
		{uint32(scard.StateChanged), "Changed"},
		{uint32(scard.StateUnknown), "Unknown"},         // reader removed
		{uint32(scard.StateUnavailable), "Unavailable"}, // reader inaccessible
		{uint32(scard.StateEmpty), "Empty"},             // no card
		{uint32(scard.StatePresent), "Present"},         // card inserted
		{uint32(scard.StateAtrmatch), "ATRMatch"},
		{uint32(scard.StateExclusive), "Exclusive"},
		{uint32(scard.StateInuse), "InUse"},
		{uint32(scard.StateMute), "Mute"}, // card not responding
		{uint32(scard.StateUnpowered), "Unpowered"},
	}
	for _, c := range checks {
		if flags&c.flag != 0 {
			active = append(active, c.name)
		}
	}

	log.Printf("[state] %-45s  raw=%-8d  counter=%-4d  flags=%s",
		name, eventState, counter, strings.Join(active, " | "))
}

// Feitian bR301 (wired)

// 2026/05/28 17:05:16 Feitian bR301 0  CurrentState[ 0 ]:  0
// 2026/05/28 17:05:16 Feitian bR301 0  EventState[ 0 ]:  18 <-- actually, it attach to pc but no card but why value is like this?

// 2026/05/28 17:07:46 Feitian bR301 0  CurrentState[ 0 ]:  0
// 2026/05/28 17:07:46 Feitian bR301 0  EventState[ 0 ]:  65826 <-- actually, it attach to pc and has a card but why value is like this?

// --------

// Feitian bR301 (bluetooth, enabled on Device Manager even turn off)

// 2026/05/28 16:53:14 FT bR301 0  CurrentState[ 0 ]:  0
// 2026/05/28 16:53:14 FT bR301 0  EventState[ 0 ]:  917522 <-- actually, it turn off but why value is like this?

// 2026/05/28 17:01:10 FT bR301 0  CurrentState[ 0 ]:  0
// 2026/05/28 17:01:10 FT bR301 0  EventState[ 0 ]:  917522 <-- actually, it turn on but no card but why value is like this?

// 2026/05/28 17:02:44 FT bR301 0  CurrentState[ 0 ]:  0
// 2026/05/28 17:02:44 FT bR301 0  EventState[ 0 ]:  917522 <-- actually, it turn on and has a card but why value is like this?

// 2026/05/28 17:05:16 FT bR301 0  CurrentState[ 0 ]:  0
// 2026/05/28 17:05:16 FT bR301 0  EventState[ 0 ]:  917522 <-- actually, it turn on but no card (wired and appare as Feitian bR301 0 instead) but why value is like this?

// 2026/05/28 17:07:46 FT bR301 0  CurrentState[ 0 ]:  0
// 2026/05/28 17:07:46 FT bR301 0  EventState[ 0 ]:  917522 <-- actually, it turn on and has a card (wired and appare as Feitian bR301 0 instead) but why value is like this?

// -----

// Identiv uTrust 2700 R Smart Card Reader (wired)

// 2026/05/28 16:53:14 Identiv uTrust 2700 R Smart Card Reader 0  CurrentState[ 0 ]:  0
// 2026/05/28 16:53:14 Identiv uTrust 2700 R Smart Card Reader 0  EventState[ 0 ]:  18 <-- actually, it attach to pc but no card but why value is like this?

// 2026/05/28 16:59:55 Identiv uTrust 2700 R Smart Card Reader 0  CurrentState[ 0 ]:  0
// 2026/05/28 16:59:55 Identiv uTrust 2700 R Smart Card Reader 0  EventState[ 0 ]:  65826 <-- actually, it attach to pc and has a card but why value is like this?
