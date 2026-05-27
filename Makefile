BINARY  := agent
SIGNAL  := wss://signal-production-b59d.up.railway.app/ws
ROOM    := demo-room-1

.PHONY: build run run-mock run-bt clean

## build — compile binary
build:
	go build -o $(BINARY) .

## run — real USB PC/SC reader
run: build
	SIGNAL_URL=$(SIGNAL) ROOM_ID=$(ROOM) ./$(BINARY)

## run-mock — stdin mock (press Enter to insert/remove card)
run-mock: build
	PCSC_MOCK=1 SIGNAL_URL=$(SIGNAL) ROOM_ID=$(ROOM) ./$(BINARY)

## run-bt — real USB + Bluetooth (btbridge must be beside binary)
run-bt: build
	SIGNAL_URL=$(SIGNAL) ROOM_ID=$(ROOM) ./$(BINARY)

## clean — remove binary
clean:
	rm -f $(BINARY)
