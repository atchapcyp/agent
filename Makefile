BINARY      := agent
BINARY_WIN  := agent.exe
SIGNAL      := wss://signal-production-b59d.up.railway.app/ws
ROOM        := demo-room-2
ORIGINS     := http://localhost:3000

.PHONY: build build-windows run run-mock run-mock-qr run-qr run-bt run-rest run-mock-rest run-rest-win run-mock-rest-win clean

## build — compile binary (current OS)
build:
	go build -o $(BINARY) .

## build-windows — compile Windows binary (must run on Windows; CGO required for PC/SC)
build-windows:
	go build -ldflags="-H windowsgui" -o $(BINARY_WIN) .

##
build-windows-dev:
	go build -o $(BINARY_WIN) .

## run — real USB PC/SC reader
run: build
	SIGNAL_URL=$(SIGNAL) ROOM_ID=$(ROOM) ./$(BINARY)

## run-mock — stdin mock (press Enter to insert/remove card)
run-mock: build
	PCSC_MOCK=1 SIGNAL_URL=$(SIGNAL) ROOM_ID=$(ROOM) ./$(BINARY)

## run-mock-qr — stdin mock + QR code printed to terminal on card insert
run-mock-qr: build
	PCSC_MOCK=1 QR_MODE=1 SIGNAL_URL=$(SIGNAL) ROOM_ID=$(ROOM) ./$(BINARY)

## run-qr — real USB PC/SC reader + QR code printed to terminal on card insert
run-qr: build
	QR_MODE=1 SIGNAL_URL=$(SIGNAL) ROOM_ID=$(ROOM) ./$(BINARY)

## run-bt — real USB + Bluetooth (btbridge must be beside binary)
run-bt: build
	SIGNAL_URL=$(SIGNAL) ROOM_ID=$(ROOM) ./$(BINARY)

## run-rest — real USB reader + local REST endpoint (GET http://127.0.0.1:47890/api/card)
run-rest: build
	TRANSPORT=rest QR_MODE=0 ALLOWED_ORIGINS=$(ORIGINS) ./$(BINARY)

## run-mock-rest — stdin mock + local REST endpoint
run-mock-rest: build
	PCSC_MOCK=1 TRANSPORT=rest QR_MODE=0 ALLOWED_ORIGINS=$(ORIGINS) ./$(BINARY)

## run-rest-win — real USB reader + REST endpoint (Windows)
run-rest-win: build-windows
	powershell -Command "$$env:TRANSPORT='rest'; $$env:QR_MODE='0'; $$env:ALLOWED_ORIGINS='$(ORIGINS)'; .\\$(BINARY_WIN)"

## run-mock-rest-win — stdin mock + REST endpoint (Windows)
run-mock-rest-win: build-windows-dev
	powershell -Command "$$env:PCSC_MOCK='1'; $$env:TRANSPORT='rest'; $$env:QR_MODE='0'; $$env:ALLOWED_ORIGINS='$(ORIGINS)'; .\\$(BINARY_WIN)"


## run-win — real PC/SC reader (Windows)
run-win: build-windows
	powershell -Command "$$env:SIGNAL_URL='$(SIGNAL)'; $$env:ROOM_ID='$(ROOM)'; .\\$(BINARY_WIN)"

## run-mock-win — stdin mock (Windows)
run-mock-win: build-windows
	powershell -Command "$$env:PCSC_MOCK='1'; $$env:SIGNAL_URL='$(SIGNAL)'; $$env:ROOM_ID='$(ROOM)'; .\\$(BINARY_WIN)"

## run-mock-qr-win
run-mock-qr-win: build-windows
	powershell -Command "$$env:PCSC_MOCK='1'; $$env:QR_MODE='1'; $$env:SIGNAL_URL='$(SIGNAL)'; $$env:ROOM_ID='$(ROOM)'; .\\$(BINARY_WIN)"

## run-qr-win
run-qr-win: build-windows
	powershell -Command "$$env:QR_MODE='1'; $$env:SIGNAL_URL='$(SIGNAL)'; $$env:ROOM_ID='$(ROOM)'; .\\$(BINARY_WIN)"

## run-win-dev — real PC/SC reader (dev) (Windows)
run-win-dev: build-windows-dev
	powershell -Command "$$env:SIGNAL_URL='$(SIGNAL)'; $$env:ROOM_ID='$(ROOM)'; .\\$(BINARY_WIN)"

## run-mock-win — stdin mock (Windows)
run-mock-win-dev: build-windows-dev
	powershell -Command "$$env:PCSC_MOCK='1'; $$env:SIGNAL_URL='$(SIGNAL)'; $$env:ROOM_ID='$(ROOM)'; .\\$(BINARY_WIN)"

## run-mock-qr-win
run-mock-qr-win-dev: build-windows-dev
	powershell -Command "$$env:PCSC_MOCK='1'; $$env:QR_MODE='1'; $$env:SIGNAL_URL='$(SIGNAL)'; $$env:ROOM_ID='$(ROOM)'; .\\$(BINARY_WIN)"

## run-qr-win
run-qr-win-dev: build-windows-dev
	powershell -Command "$$env:QR_MODE='1'; $$env:SIGNAL_URL='$(SIGNAL)'; $$env:ROOM_ID='$(ROOM)'; .\\$(BINARY_WIN)"

## clean — remove binaries
clean:
	rm -f $(BINARY) $(BINARY_WIN)
