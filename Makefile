BINARY      := agent
BINARY_WIN  := agent.exe
SIGNAL      := wss://signal-production-b59d.up.railway.app/ws
ROOM        := demo-room-1

.PHONY: build build-windows run run-mock run-bt clean

## build — compile binary (current OS)
build:
	go build -o $(BINARY) .

## build-windows — compile Windows binary (must run on Windows; CGO required for PC/SC)
build-windows:
	go build -ldflags="-H windowsgui" -o $(BINARY_WIN) .

## run — real USB PC/SC reader
run: build
	SIGNAL_URL=$(SIGNAL) ROOM_ID=$(ROOM) ./$(BINARY)

## run-mock — stdin mock (press Enter to insert/remove card)
run-mock: build
	PCSC_MOCK=1 SIGNAL_URL=$(SIGNAL) ROOM_ID=$(ROOM) ./$(BINARY)

## run-bt — real USB + Bluetooth (btbridge must be beside binary)
run-bt: build
	SIGNAL_URL=$(SIGNAL) ROOM_ID=$(ROOM) ./$(BINARY)

## clean — remove binaries
clean:
	rm -f $(BINARY) $(BINARY_WIN)
