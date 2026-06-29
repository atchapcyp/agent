package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/mdp/qrterminal/v3"
	"github.com/ntl/thai-id-card-reader/agent/httpapi"
	"github.com/ntl/thai-id-card-reader/agent/pcsc"
	"github.com/ntl/thai-id-card-reader/agent/pcsc/real"
	agentsignaling "github.com/ntl/thai-id-card-reader/agent/signaling"
	agentwebrtc "github.com/ntl/thai-id-card-reader/agent/webrtc"
)

func main() {
	signalURL := getEnv("SIGNAL_URL", "wss://signal-production-b59d.up.railway.app/ws")
	roomID := getEnv("ROOM_ID", "demo-room-1")

	qrMode := getEnv("QR_MODE", "1") == "1"

	// Transport: "rest" (default) serves card over loopback REST; "webrtc"
	// uses the legacy signaling + data-channel broadcast path.
	transport := getEnv("TRANSPORT", "rest")
	restMode := transport != "webrtc"

	log.Printf("[agent] starting — transport=%s signal=%s room=%s qr_mode=%v", transport, signalURL, roomID, qrMode)

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

	// REST transport: serve current card over loopback for browser fetch.
	var api *httpapi.Server
	if restMode {
		port := getEnv("REST_PORT", "8080")
		origins := splitOrigins(getEnv("ALLOWED_ORIGINS", ""))
		api = httpapi.New(port, origins)
		go func() {
			if err := api.Start(); err != nil {
				log.Fatalf("[agent] http api fatal: %v", err)
			}
		}()
	} else if !qrMode {
		// WebRTC transport: connect to signaling server (auto-reconnects).
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
				log.Println("[agent] card inserted but read failed — skipping")
				continue
			}
			if qrMode {
				printCardQR(event.Data)
			}
			if restMode {
				api.SetCard(event.Data)
			} else {
				manager.Broadcast(map[string]any{
					"event": "card_inserted",
					"data":  event.Data,
				})
			}
		case "card_removed":
			if qrMode {
				fmt.Println("[qr] Card removed.")
			}
			if restMode {
				api.ClearCard()
			} else {
				manager.Broadcast(map[string]any{
					"event": "card_removed",
				})
			}
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

// splitOrigins parses a comma-separated ALLOWED_ORIGINS value into a slice,
// trimming spaces and dropping empties.
func splitOrigins(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
