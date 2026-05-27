// Package webrtc manages RTCPeerConnections — one per connected browser tab.
package webrtc

import (
	"encoding/json"
	"log"
	"sync"

	"github.com/pion/webrtc/v3"
)

// Sender abstracts the signaling client so manager can send offers/candidates.
type Sender interface {
	SendOffer(toPeerID, sdp string)
	SendCandidate(toPeerID string, payload any)
}

// peer holds a single browser connection.
type peer struct {
	pc *webrtc.PeerConnection
	dc *webrtc.DataChannel
}

// Manager maintains all active WebRTC peer connections.
type Manager struct {
	mu     sync.RWMutex
	peers  map[string]*peer // browserPeerID → peer
	signal Sender
	api    *webrtc.API
}

func NewManager(signal Sender) *Manager {
	// Use default API (no media — data channel only)
	return &Manager{
		peers:  make(map[string]*peer),
		signal: signal,
		api:    webrtc.NewAPI(),
	}
}

var stunConfig = webrtc.Configuration{
	ICEServers: []webrtc.ICEServer{
		{URLs: []string{"stun:stun.l.google.com:19302"}},
	},
}

// HandlePeerJoined creates a new PeerConnection + DataChannel and sends offer.
func (m *Manager) HandlePeerJoined(browserPeerID string) {
	pc, err := m.api.NewPeerConnection(stunConfig)
	if err != nil {
		log.Printf("[webrtc] failed to create PC for peer=%s: %v", browserPeerID, err)
		return
	}

	// Create the data channel (agent is the offerer, so agent creates DC)
	dc, err := pc.CreateDataChannel("card-reader", &webrtc.DataChannelInit{
		Ordered: boolPtr(true),
	})
	if err != nil {
		log.Printf("[webrtc] failed to create DC for peer=%s: %v", browserPeerID, err)
		pc.Close()
		return
	}

	dc.OnOpen(func() {
		log.Printf("[webrtc] DataChannel open — peer=%s", browserPeerID)
	})
	dc.OnClose(func() {
		log.Printf("[webrtc] DataChannel closed — peer=%s", browserPeerID)
	})

	p := &peer{pc: pc, dc: dc}

	// Send ICE candidates as they are gathered (full ICECandidateInit — sdpMid + sdpMLineIndex required by browsers)
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return // gathering complete
		}
		m.signal.SendCandidate(browserPeerID, c.ToJSON())
	})

	// Clean up on connection failure
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("[webrtc] peer=%s state=%s", browserPeerID, state)
		if state == webrtc.PeerConnectionStateFailed ||
			state == webrtc.PeerConnectionStateDisconnected ||
			state == webrtc.PeerConnectionStateClosed {
			m.removePeer(browserPeerID)
		}
	})

	// Create and send offer
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		log.Printf("[webrtc] create offer failed for peer=%s: %v", browserPeerID, err)
		pc.Close()
		return
	}
	if err := pc.SetLocalDescription(offer); err != nil {
		log.Printf("[webrtc] set local desc failed for peer=%s: %v", browserPeerID, err)
		pc.Close()
		return
	}

	m.mu.Lock()
	m.peers[browserPeerID] = p
	m.mu.Unlock()

	log.Printf("[webrtc] sending offer to peer=%s", browserPeerID)
	m.signal.SendOffer(browserPeerID, offer.SDP)
}

// HandlePeerLeft closes and removes a peer connection.
func (m *Manager) HandlePeerLeft(browserPeerID string) {
	m.removePeer(browserPeerID)
}

// HandleAnswer sets the remote description from the browser's answer.
func (m *Manager) HandleAnswer(fromPeerID, sdp string) {
	m.mu.RLock()
	p, ok := m.peers[fromPeerID]
	m.mu.RUnlock()
	if !ok {
		log.Printf("[webrtc] answer from unknown peer=%s", fromPeerID)
		return
	}
	answer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  sdp,
	}
	if err := p.pc.SetRemoteDescription(answer); err != nil {
		log.Printf("[webrtc] set remote desc failed for peer=%s: %v", fromPeerID, err)
	}
}

// HandleCandidate adds an ICE candidate from the browser.
func (m *Manager) HandleCandidate(fromPeerID string, candidateJSON json.RawMessage) {
	m.mu.RLock()
	p, ok := m.peers[fromPeerID]
	m.mu.RUnlock()
	if !ok {
		return
	}
	var init webrtc.ICECandidateInit
	if err := json.Unmarshal(candidateJSON, &init); err != nil {
		log.Printf("[webrtc] bad candidate JSON from peer=%s: %v", fromPeerID, err)
		return
	}
	if err := p.pc.AddICECandidate(init); err != nil {
		log.Printf("[webrtc] add ICE candidate failed for peer=%s: %v", fromPeerID, err)
	}
}

// Broadcast sends a JSON message to all connected browser DataChannels.
func (m *Manager) Broadcast(msg any) {
	b, err := json.Marshal(msg)
	if err != nil {
		log.Printf("[webrtc] broadcast marshal error: %v", err)
		return
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	sent := 0
	for peerID, p := range m.peers {
		if p.dc.ReadyState() == webrtc.DataChannelStateOpen {
			if err := p.dc.SendText(string(b)); err != nil {
				log.Printf("[webrtc] send error peer=%s: %v", peerID, err)
			} else {
				sent++
			}
		}
	}
	log.Printf("[webrtc] broadcast sent to %d peer(s)", sent)
}

func (m *Manager) removePeer(peerID string) {
	m.mu.Lock()
	p, ok := m.peers[peerID]
	delete(m.peers, peerID)
	m.mu.Unlock()

	if ok {
		p.dc.Close()
		p.pc.Close()
		log.Printf("[webrtc] removed peer=%s", peerID)
	}
}

func boolPtr(b bool) *bool { return &b }
