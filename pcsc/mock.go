// Mock adapter — simulates card insert/remove via stdin.
// Press Enter to insert card. Press Enter again to remove.
package pcsc

import (
	"bufio"
	"fmt"
	"os"
)

// MockReader is the stdin-driven card reader adapter for demo/testing.
type MockReader struct{}

// NewMock returns a Reader backed by stdin keypresses.
func NewMock() Reader { return &MockReader{} }

var mockCard = CitizenInfo{
	CitizenID:    ptr("1101700203456"),
	TitleTH:      ptr("นาย"),
	FirstNameTH:  ptr("สิทธิเดช"),
	MiddleNameTH: ptr(""),
	LastNameTH:   ptr("ปวุตินันท์"),

	TitleEN:      ptr("Mr."),
	FirstNameEN:  ptr("Sittidet"),
	MiddleNameEN: ptr(""),
	LastNameEN:   ptr("Pawutinan"),

	DOB:        ptr("25440525"),
	Gender:     ptr(GenderMale),
	CardIssuer: ptr("ท้องถิ่นเขตปทุมวัน/กรุงเทพมหานคร"),
	IssueDate:  ptr("25680727"),
	ExpireDate: ptr("25770524"),

	Address: &Address{
		HouseNo:     ptr("236/172"),
		Moo:         ptr("10"),
		Alley:       ptr(""),
		Soi:         ptr("สุขุมวิท 24"),
		Road:        ptr("สุขุมวิท"),
		SubDistrict: ptr("คลองตัน"),
		District:    ptr("คลองเตย"),
		Province:    ptr("กรุงเทพมหานคร"),
	},
}

func ptr[T any](v T) *T {
	return &v
}

func (m *MockReader) Watch(ch chan<- Event) error {
	fmt.Println("[pcsc/mock] Press ENTER to insert card. Press ENTER again to remove.")
	scanner := bufio.NewScanner(os.Stdin)
	inserted := false
	for scanner.Scan() {
		if !inserted {
			fmt.Println("[pcsc/mock] Card INSERTED")
			card := mockCard
			ch <- Event{Type: "card_inserted", Data: &card}
			inserted = true
		} else {
			fmt.Println("[pcsc/mock] Card REMOVED")
			ch <- Event{Type: "card_removed"}
			inserted = false
		}
	}
	return scanner.Err()
}
