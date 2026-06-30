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

type Gender string

const (
	GenderMale   Gender = "MALE"
	GenderFemale Gender = "FEMALE"
)

type CitizenInfo struct {
	CitizenID    *string  `json:"citizenId,omitempty"`
	TitleTH      *string  `json:"titleTH,omitempty"`
	FirstNameTH  *string  `json:"firstNameTH,omitempty"`
	MiddleNameTH *string  `json:"middleNameTH,omitempty"`
	LastNameTH   *string  `json:"lastNameTH,omitempty"`
	TitleEN      *string  `json:"titleEN,omitempty"`
	FirstNameEN  *string  `json:"firstNameEN,omitempty"`
	MiddleNameEN *string  `json:"middleNameEN,omitempty"`
	LastNameEN   *string  `json:"lastNameEN,omitempty"`
	DOB          *string  `json:"dob,omitempty"`
	Gender       *Gender  `json:"gender,omitempty"`
	CardIssuer   *string  `json:"cardIssuer,omitempty"`
	IssueDate    *string  `json:"issueDate,omitempty"`
	ExpireDate   *string  `json:"expireDate,omitempty"`
	Address      *Address `json:"address,omitempty"`
}

type Address struct {
	HouseNo     *string `json:"houseNo,omitempty"`
	Moo         *string `json:"moo,omitempty"`
	Alley       *string `json:"alley,omitempty"`
	Soi         *string `json:"soi,omitempty"`
	Road        *string `json:"road,omitempty"`
	SubDistrict *string `json:"subDistrict,omitempty"`
	District    *string `json:"district,omitempty"`
	Province    *string `json:"province,omitempty"`
}

// Event is emitted by any Reader adapter.
type Event struct {
	Type string       // "card_inserted" | "card_removed"
	Data *CitizenInfo // non-nil only for card_inserted
}
