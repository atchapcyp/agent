// Real PC/SC adapter — reads Thai ID cards via USB (ebfe/scard) and/or Bluetooth (btbridge).
// Both transports run concurrently and write to the same event channel.
// Auto-detection: USB polls until a reader appears; BT is skipped if binary not found.
//
// Env vars:
//
//	BTBRIDGE_PATH  path to btbridge binary (default: ./btbridge beside the executable)
package real

import (
	"bytes"
	"fmt"
	"log"
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
				}
			}

			return readers[0], nil
		}
		log.Println("[pcsc/usb] no reader found, retrying in 2s…")
		time.Sleep(2 * time.Second)
	}
}

func readCardUSB(ctx *scard.Context, readerName string) (*pcsc.CitizenInfo, error) {
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
	cid := trimASCII(cidBytes)

	nameTHBytes, err := execAPDU(card, cmdNameTH)
	if err != nil {
		fmt.Println(fmt.Errorf("READ NameTH: %w", err))
	}
	titleTH, firstNameTH, middleNameTH, lastNameTH := splitFullName(decodeTIS620(nameTHBytes))

	nameENBytes, err := execAPDU(card, cmdNameEN)
	if err != nil {
		fmt.Println(fmt.Errorf("READ NameEN: %w", err))
	}
	titleEN, firstNameEN, middleNameEN, lastNameEN := splitFullName(decodeTIS620(nameENBytes))

	dobBytes, err := execAPDU(card, cmdDOB)
	if err != nil {
		fmt.Println(fmt.Errorf("READ DOB: %w", err))
	}
	dob := trimASCII(dobBytes)

	addressBytes, err := execAPDU(card, cmdAddress)
	if err != nil {
		fmt.Println(fmt.Errorf("READ Address: %w", err))
	}
	address := decodeAddress(decodeTIS620(addressBytes))

	genderBytes, err := execAPDU(card, cmdGender)
	if err != nil {
		fmt.Println(fmt.Errorf("READ Gender: %w", err))
	}
	gender := decodeGender(trimASCII(genderBytes))

	issueDateBytes, err := execAPDU(card, cmdIssueDate)
	if err != nil {
		fmt.Println(fmt.Errorf("READ IssueDate: %w", err))
	}
	issueDate := trimASCII(issueDateBytes)

	expireDateBytes, err := execAPDU(card, cmdExpireDate)
	if err != nil {
		fmt.Println(fmt.Errorf("READ ExpireDate: %w", err))
	}
	expireDate := trimASCII(expireDateBytes)

	cardIssuerBytes, err := execAPDU(card, cmdCardIssuer)
	if err != nil {
		fmt.Println(fmt.Errorf("READ CardIssuer: %w", err))
	}
	cardIssuer := decodeTIS620(cardIssuerBytes)

	return &pcsc.CitizenInfo{
		CitizenID:    &cid,
		TitleTH:      &titleTH,
		FirstNameTH:  &firstNameTH,
		MiddleNameTH: &middleNameTH,
		LastNameTH:   &lastNameTH,
		TitleEN:      &titleEN,
		FirstNameEN:  &firstNameEN,
		MiddleNameEN: &middleNameEN,
		LastNameEN:   &lastNameEN,
		DOB:          &dob,
		Address:      address,
		Gender:       gender,
		IssueDate:    &issueDate,
		ExpireDate:   &expireDate,
		CardIssuer:   &cardIssuer,
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

func splitFullName(fullName string) (string, string, string, string) {
	splitedNames := strings.Split(fullName, "#")
	var title string
	if len(splitedNames) > 0 && len(splitedNames[0]) > 0 {
		title = splitedNames[0]
	}
	var firstName string
	if len(splitedNames) > 1 && len(splitedNames[1]) > 0 {
		firstName = splitedNames[1]
	}
	var middleName string
	if len(splitedNames) > 2 && len(splitedNames[2]) > 0 {
		middleName = splitedNames[2]
	}
	var lastName string
	if len(splitedNames) > 3 && len(splitedNames[3]) > 0 {
		lastName = splitedNames[3]
	}
	return title, firstName, middleName, lastName
}

func decodeGender(baseGender string) *pcsc.Gender {
	var g pcsc.Gender
	switch baseGender {
	case "1":
		g = pcsc.GenderMale
	case "2":
		g = pcsc.GenderFemale
	}
	return &g
}

func decodeAddress(baseAddress string) *pcsc.Address {
	splitedBaseAddress := strings.Split(baseAddress, "#")

	var houseNo *string
	if len(splitedBaseAddress) > 0 && len(splitedBaseAddress[0]) > 0 {
		houseNo = &splitedBaseAddress[0]
	}
	var moo *string
	if len(splitedBaseAddress) > 1 && len(splitedBaseAddress[1]) > 0 {
		moo = &splitedBaseAddress[1]
	}
	var alley *string
	if len(splitedBaseAddress) > 2 && len(splitedBaseAddress[2]) > 0 {
		alley = &splitedBaseAddress[2]
	}
	var soi *string
	if len(splitedBaseAddress) > 3 && len(splitedBaseAddress[3]) > 0 {
		soi = &splitedBaseAddress[3]
	}
	var road *string
	if len(splitedBaseAddress) > 4 && len(splitedBaseAddress[4]) > 0 {
		road = &splitedBaseAddress[4]
	}
	var subDistrict *string
	if len(splitedBaseAddress) > 5 && len(splitedBaseAddress[5]) > 0 {
		subDistrict = &splitedBaseAddress[5]
	}
	var district *string
	if len(splitedBaseAddress) > 6 && len(splitedBaseAddress[6]) > 0 {
		district = &splitedBaseAddress[6]
	}
	var province *string
	if len(splitedBaseAddress) > 7 && len(splitedBaseAddress[7]) > 0 {
		province = &splitedBaseAddress[7]
	}
	return &pcsc.Address{
		HouseNo:     houseNo,
		Moo:         moo,
		Alley:       alley,
		Soi:         soi,
		Road:        road,
		SubDistrict: subDistrict,
		District:    district,
		Province:    province,
	}
}
