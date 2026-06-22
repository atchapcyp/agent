package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/mdp/qrterminal/v3"
	"github.com/ntl/thai-id-card-reader/agent/pcsc"
	"github.com/ntl/thai-id-card-reader/agent/pcsc/real"
	agentsignaling "github.com/ntl/thai-id-card-reader/agent/signaling"
	agentwebrtc "github.com/ntl/thai-id-card-reader/agent/webrtc"
)

func main() {
	signalURL := getEnv("SIGNAL_URL", "wss://signal-production-b59d.up.railway.app/ws")
	roomID := getEnv("ROOM_ID", "demo-room-1")

	qrMode := getEnv("QR_MODE", "1") == "1"

	log.Printf("[agent] starting — signal=%s room=%s qr_mode=%v", signalURL, roomID, qrMode)

	// ── Select PC/SC adapter ─────────────────────────────────────────────────
	// PCSC_MOCK=1  → stdin mock (press Enter to insert/remove card)
	// PCSC_MOCK=0  → real PC/SC hardware (default)
	var reader pcsc.Reader
	if getEnv("PCSC_MOCK", "0") == "1" {
		log.Println("[agent] using MOCK card reader (stdin)")
		reader = pcsc.NewMock()
	} else {
		log.Println("[agent] using REAL PC/SC card reader")
		reader = real.New()
	}

	// ── Wire up components ────────────────────────────────────────────────────
	var sigClient *agentsignaling.Client
	var manager *agentwebrtc.Manager

	sigClient = agentsignaling.NewClient(signalURL, roomID, agentsignaling.Callbacks{
		OnReady: func(myPeerID string) {
			log.Printf("[agent] ready in room=%s as peer=%s", roomID, myPeerID)
		},
		OnPeerJoined: func(browserPeerID string) {
			manager.HandlePeerJoined(browserPeerID)
		},
		OnPeerLeft: func(browserPeerID string) {
			manager.HandlePeerLeft(browserPeerID)
		},
		OnAnswer: func(fromPeerID, sdp string) {
			manager.HandleAnswer(fromPeerID, sdp)
		},
		OnCandidate: func(fromPeerID string, candidateJSON json.RawMessage) {
			manager.HandleCandidate(fromPeerID, candidateJSON)
		},
	})

	manager = agentwebrtc.NewManager(sigClient)

	if !qrMode {
		// Connect to signaling server (auto-reconnects)
		go sigClient.Connect()
	}

	// Start card reader — blocks until fatal error
	cardEvents := make(chan pcsc.Event, 4)
	go func() {
		if err := reader.Watch(cardEvents); err != nil {
			log.Fatalf("[agent] card reader fatal: %v", err)
		}
	}()

	// Handle card events → broadcast to all connected browsers
	for event := range cardEvents {
		switch event.Type {
		case "card_inserted":
			if event.Data == nil {
				log.Println("[agent] card inserted but read failed — skipping broadcast")
				continue
			}
			if qrMode {
				printCardQR(event.Data)
			}
			manager.Broadcast(map[string]any{
				"event": "card_inserted",
				"data":  event.Data,
			})
		case "card_removed":
			if qrMode {
				fmt.Println("[qr] Card removed.")
			}
			manager.Broadcast(map[string]any{
				"event": "card_removed",
			})
		}
	}
}

func printCardQR(data *pcsc.CardData) {
	b, err := json.Marshal(data)
	if err != nil {
		log.Printf("[qr] marshal failed: %v", err)
		return
	}
	fmt.Println("\n── QR Code ─────────────────────────────────────────────")
	qrterminal.GenerateHalfBlock(string(b), qrterminal.L, os.Stdout)
	fmt.Println("────────────────────────────────────────────────────────")
	fmt.Printf("[qr] payload: %s\n\n", b)
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
