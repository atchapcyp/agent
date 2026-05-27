// Package signaling implements a WebSocket client for the signaling server.
package signaling

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/gorilla/websocket"
)

// Message is the envelope for all signaling messages.
type Message struct {
	// Server → client events
	Type        string `json:"type"`
	AgentPeerID string `json:"agentPeerId,omitempty"`
	YourPeerID  string `json:"yourPeerId,omitempty"`
	PeerID      string `json:"peerId,omitempty"`

	// Directed WebRTC messages
	From    string          `json:"from,omitempty"`
	To      string          `json:"to,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Callbacks fired by the client on incoming events.
type Callbacks struct {
	OnReady     func(myPeerID string)                       // room_info received, agent connected
	OnPeerJoined func(browserPeerID string)                  // browser joined room
	OnPeerLeft   func(browserPeerID string)                  // browser left room
	OnAnswer     func(fromPeerID string, sdp string)         // SDP answer from browser
	OnCandidate  func(fromPeerID string, candidateJSON json.RawMessage) // ICE candidate from browser
}

// Client manages a WebSocket connection to the signaling server.
type Client struct {
	url      string
	roomID   string
	myPeerID string
	conn     *websocket.Conn
	send     chan []byte
	cb       Callbacks
}

func NewClient(serverURL, roomID string, cb Callbacks) *Client {
	return &Client{
		url:    fmt.Sprintf("%s?room=%s&role=agent", serverURL, roomID),
		roomID: roomID,
		send:   make(chan []byte, 256),
		cb:     cb,
	}
}

// Connect establishes the WebSocket connection and blocks, reconnecting on disconnect.
func (c *Client) Connect() {
	for {
		if err := c.connect(); err != nil {
			log.Printf("[signaling] disconnected: %v — reconnecting in 3s", err)
		}
		time.Sleep(3 * time.Second)
	}
}

func (c *Client) connect() error {
	conn, _, err := websocket.DefaultDialer.Dial(c.url, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	c.conn = conn
	log.Printf("[signaling] connected to %s", c.url)

	// drain send channel on reconnect
	c.send = make(chan []byte, 256)

	go c.writePump()
	return c.readPump()
}

func (c *Client) readPump() error {
	defer c.conn.Close()
	for {
		_, raw, err := c.conn.ReadMessage()
		if err != nil {
			return err
		}

		var msg Message
		if err := json.Unmarshal(raw, &msg); err != nil {
			log.Printf("[signaling] bad message: %v", err)
			continue
		}

		switch msg.Type {
		case "room_info":
			c.myPeerID = msg.YourPeerID
			log.Printf("[signaling] ready — peer_id=%s", c.myPeerID)
			if c.cb.OnReady != nil {
				c.cb.OnReady(c.myPeerID)
			}

		case "peer_joined":
			log.Printf("[signaling] browser joined: %s", msg.PeerID)
			if c.cb.OnPeerJoined != nil {
				c.cb.OnPeerJoined(msg.PeerID)
			}

		case "peer_left":
			log.Printf("[signaling] browser left: %s", msg.PeerID)
			if c.cb.OnPeerLeft != nil {
				c.cb.OnPeerLeft(msg.PeerID)
			}

		// Forwarded directed messages
		default:
			if msg.From == "" {
				continue
			}
			switch msg.Type {
			case "answer":
				var sdp struct {
					SDP string `json:"sdp"`
				}
				if err := json.Unmarshal(msg.Payload, &sdp); err != nil {
					log.Printf("[signaling] bad answer payload: %v", err)
					continue
				}
				if c.cb.OnAnswer != nil {
					c.cb.OnAnswer(msg.From, sdp.SDP)
				}

			case "candidate":
				if c.cb.OnCandidate != nil {
					c.cb.OnCandidate(msg.From, msg.Payload)
				}
			}
		}
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				return
			}
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				log.Printf("[signaling] write error: %v", err)
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			c.conn.WriteMessage(websocket.PingMessage, nil)
		}
	}
}

// SendOffer sends an SDP offer to a browser peer.
func (c *Client) SendOffer(toPeerID, sdp string) {
	c.sendMsg(toPeerID, "offer", map[string]string{"sdp": sdp})
}

// SendCandidate sends an ICE candidate (full ICECandidateInit) to a browser peer.
func (c *Client) SendCandidate(toPeerID string, payload any) {
	c.sendMsg(toPeerID, "candidate", payload)
}

func (c *Client) sendMsg(to, msgType string, payload any) {
	p, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[signaling] marshal error: %v", err)
		return
	}
	msg := Message{
		From:    c.myPeerID,
		To:      to,
		Type:    msgType,
		Payload: p,
	}
	b, _ := json.Marshal(msg)
	select {
	case c.send <- b:
	default:
		log.Printf("[signaling] send buffer full")
	}
}

// MyPeerID returns this agent's peer_id (empty until OnReady fires).
func (c *Client) MyPeerID() string {
	return c.myPeerID
}
