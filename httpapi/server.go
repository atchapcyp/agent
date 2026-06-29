// Package httpapi serves the current dip-chip card on a loopback REST endpoint.
//
// Browser frontends fetch GET /api/thai-nat-id-card to pull the latest read.
// The server binds to 127.0.0.1 only — off-box clients cannot reach it. PII is
// protected by an Origin allowlist (deny-all by default), so an arbitrary
// website the user visits cannot silently read the card.
//
// Env vars:
//
//	REST_PORT        listen port on 127.0.0.1 (default: 7000)
//	ALLOWED_ORIGINS  comma-separated exact origins allowed via CORS
//	                 (e.g. "https://app.example.com,https://staging.example.com")
//	                 empty = deny all cross-origin reads
package httpapi

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/ntl/thai-id-card-reader/agent/pcsc"
)

const cardPath = "/api/thai-nat-id-card"

// Server holds the latest card state and serves it over HTTP.
type Server struct {
	mu   sync.RWMutex
	card *pcsc.CardData

	addr           string
	allowedOrigins map[string]struct{}
}

// New builds a Server listening on 127.0.0.1:port. allowedOrigins is the exact
// set of Origin header values permitted to read responses; an empty set denies
// all cross-origin reads.
func New(port string, allowedOrigins []string) *Server {
	origins := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		o = strings.TrimSpace(o)
		if o != "" {
			origins[o] = struct{}{}
		}
	}
	return &Server{
		addr:           "127.0.0.1:" + port,
		allowedOrigins: origins,
	}
}

// SetCard stores the latest successfully read card.
func (s *Server) SetCard(card *pcsc.CardData) {
	s.mu.Lock()
	s.card = card
	s.mu.Unlock()
}

// ClearCard drops the stored card (called on card removal).
func (s *Server) ClearCard() {
	s.mu.Lock()
	s.card = nil
	s.mu.Unlock()
}

// Start runs the HTTP server. Blocks until the server exits.
func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc(cardPath, s.handleCard)
	log.Printf("[httpapi] listening on http://%s%s — allowed origins: %d", s.addr, cardPath, len(s.allowedOrigins))
	return http.ListenAndServe(s.addr, mux)
}

// dataEnvelope wraps a successful read: {"data": {...CardData}}.
type dataEnvelope struct {
	Data *pcsc.CardData `json:"data"`
}

// errorEnvelope wraps a failure: {"error": {"message": "..."}}.
type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Message string `json:"message"`
}

func (s *Server) handleCard(w http.ResponseWriter, r *http.Request) {
	s.applyCORS(w, r)

	if r.Method == http.MethodOptions {
		// CORS preflight (incl. Chrome Private Network Access).
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET, OPTIONS")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.RLock()
	card := s.card
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")

	if card == nil {
		w.WriteHeader(http.StatusNotFound)
		writeJSON(w, errorEnvelope{Error: errorBody{Message: "fail to read information from the id card"}})
		return
	}
	writeJSON(w, dataEnvelope{Data: card})
}

func writeJSON(w http.ResponseWriter, v any) {
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("[httpapi] encode error: %v", err)
	}
}

// applyCORS echoes the request Origin only if it is in the allowlist, and
// answers Chrome's Private Network Access preflight. No credentials/cookies
// are used, so Access-Control-Allow-Credentials is intentionally omitted.
func (s *Server) applyCORS(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return
	}
	if _, ok := s.allowedOrigins[origin]; !ok {
		return // not allowlisted — browser will block the read
	}

	h := w.Header()
	h.Set("Access-Control-Allow-Origin", origin)
	h.Add("Vary", "Origin")
	h.Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	h.Set("Access-Control-Allow-Headers", "Content-Type")

	// Chrome/Edge Private Network Access: public HTTPS page → loopback.
	if r.Header.Get("Access-Control-Request-Private-Network") == "true" {
		h.Set("Access-Control-Allow-Private-Network", "true")
	}
}
