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

var mockCard = CardData{
	CID:        "1234567890123",
	NameTH:     "นาย สมชาย ใจดี",
	NameEN:     "MR. SOMCHAI JAIDEE",
	DOB:        "19900115",
	Gender:     "1",
	CardIssuer: "สำนักงานเขตบางรัก",
	IssueDate:  "20200101",
	ExpireDate: "20300101",
	Address:    "123 ถนนสีลม แขวงสีลม เขตบางรัก กรุงเทพมหานคร 10500",
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
