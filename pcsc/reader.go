// Package pcsc defines the CardReader port and shared types.
// Adapters (mock, real) implement the Reader interface.
package pcsc

// Reader is the port — any card reader adapter must implement this.
type Reader interface {
	// Watch blocks and sends card events to ch.
	// Returns a fatal error if the reader can no longer recover.
	Watch(ch chan<- Event) error
}

// CardData represents Thai ID card fields.
type CardData struct {
	CID        string `json:"cid"`
	NameTH     string `json:"nameTH"`
	NameEN     string `json:"nameEN"`
	DOB        string `json:"dob"`
	Gender     string `json:"gender,omitempty"`
	CardIssuer string `json:"cardIssuer,omitempty"`
	IssueDate  string `json:"issueDate,omitempty"`
	ExpireDate string `json:"expireDate,omitempty"`
	Address    string `json:"address"`
}

// Event is emitted by any Reader adapter.
type Event struct {
	Type string    // "card_inserted" | "card_removed"
	Data *CardData // non-nil only for card_inserted
}
