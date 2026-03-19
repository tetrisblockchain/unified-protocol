package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const (
	DefaultWSPollInterval = 2 * time.Second
	DefaultWSResultLimit  = 8
	MaxWSResultLimit      = 64
	MaxWSMessageBytes     = 1 << 20
)

var defaultWSTopics = []string{"health", "chain", "blocks", "mempool", "peers", "governance"}

type WebSocketConfig struct {
	AllowedOrigins []string
	Logger         *log.Logger
	PollInterval   time.Duration

	Health      func() any
	ChainStatus func() any
	LatestBlock func() any
	Mempool     func(limit int) any
	Peers       func() any
	Governance  func() any
}

type WebSocketServer struct {
	logger       *log.Logger
	pollInterval time.Duration
	origins      []string
	health       func() any
	chainStatus  func() any
	latestBlock  func() any
	mempool      func(limit int) any
	peers        func() any
	governance   func() any
	upgrader     websocket.Upgrader
}

type wsEnvelope struct {
	Type       string `json:"type"`
	Topic      string `json:"topic,omitempty"`
	ServerTime string `json:"serverTime"`
	Data       any    `json:"data,omitempty"`
	Error      string `json:"error,omitempty"`
}

func NewWebSocketServer(config WebSocketConfig) *WebSocketServer {
	logger := config.Logger
	if logger == nil {
		logger = log.Default()
	}
	origins := normalizeOrigins(config.AllowedOrigins)
	server := &WebSocketServer{
		logger:       logger,
		pollInterval: config.PollInterval,
		origins:      origins,
		health:       config.Health,
		chainStatus:  config.ChainStatus,
		latestBlock:  config.LatestBlock,
		mempool:      config.Mempool,
		peers:        config.Peers,
		governance:   config.Governance,
	}
	if server.pollInterval <= 0 {
		server.pollInterval = DefaultWSPollInterval
	}
	server.upgrader = websocket.Upgrader{
		CheckOrigin: server.checkOrigin,
	}
	return server
}

func (s *WebSocketServer) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	conn, err := s.upgrader.Upgrade(writer, request, nil)
	if err != nil {
		s.logger.Printf("websocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(request.Context())
	defer cancel()

	topics := normalizeWSTopics(request.URL.Query().Get("topics"))
	limit := normalizeWSLimit(request.URL.Query().Get("limit"))
	conn.SetReadLimit(MaxWSMessageBytes)

	writeErrs := make(chan error, 1)
	send := make(chan wsEnvelope, 16)
	go func() {
		for envelope := range send {
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteJSON(envelope); err != nil {
				writeErrs <- err
				cancel()
				return
			}
		}
		writeErrs <- nil
	}()

	go func() {
		defer cancel()
		for {
			if _, _, err := conn.NextReader(); err != nil {
				return
			}
		}
	}()

	if !s.pushEnvelope(ctx, send, wsEnvelope{
		Type:       "hello",
		ServerTime: time.Now().UTC().Format(time.RFC3339Nano),
		Data: map[string]any{
			"topics":    topics,
			"available": defaultWSTopics,
			"limit":     limit,
		},
	}) {
		return
	}

	lastSent := make(map[string]string, len(topics))
	for _, topic := range topics {
		if !s.pushTopicSnapshot(ctx, send, lastSent, topic, limit, true) {
			return
		}
	}

	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			close(send)
			<-writeErrs
			return
		case err := <-writeErrs:
			if err != nil {
				s.logger.Printf("websocket write failed: %v", err)
			}
			return
		case <-ticker.C:
			for _, topic := range topics {
				if !s.pushTopicSnapshot(ctx, send, lastSent, topic, limit, false) {
					return
				}
			}
		}
	}
}

func (s *WebSocketServer) checkOrigin(request *http.Request) bool {
	if request == nil {
		return true
	}
	origin := strings.TrimSpace(request.Header.Get("Origin"))
	if origin == "" || len(s.origins) == 0 {
		return true
	}
	_, ok := matchOrigin(origin, s.origins)
	return ok
}

func (s *WebSocketServer) pushTopicSnapshot(
	ctx context.Context,
	send chan<- wsEnvelope,
	lastSent map[string]string,
	topic string,
	limit int,
	force bool,
) bool {
	payload, ok := s.snapshot(topic, limit)
	if !ok {
		return true
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		s.logger.Printf("websocket marshal failed for %s: %v", topic, err)
		return true
	}
	current := string(encoded)
	if !force && lastSent[topic] == current {
		return true
	}
	lastSent[topic] = current
	return s.pushEnvelope(ctx, send, wsEnvelope{
		Type:       "snapshot",
		Topic:      topic,
		ServerTime: time.Now().UTC().Format(time.RFC3339Nano),
		Data:       payload,
	})
}

func (s *WebSocketServer) pushEnvelope(ctx context.Context, send chan<- wsEnvelope, envelope wsEnvelope) bool {
	select {
	case <-ctx.Done():
		return false
	case send <- envelope:
		return true
	}
}

func (s *WebSocketServer) snapshot(topic string, limit int) (any, bool) {
	switch topic {
	case "health":
		if s.health == nil {
			return nil, false
		}
		return s.health(), true
	case "chain":
		if s.chainStatus == nil {
			return nil, false
		}
		return s.chainStatus(), true
	case "blocks":
		if s.latestBlock == nil {
			return nil, false
		}
		return s.latestBlock(), true
	case "mempool":
		if s.mempool == nil {
			return nil, false
		}
		return s.mempool(limit), true
	case "peers":
		if s.peers == nil {
			return nil, false
		}
		return s.peers(), true
	case "governance":
		if s.governance == nil {
			return nil, false
		}
		return s.governance(), true
	default:
		return nil, false
	}
}

func normalizeWSTopics(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return append([]string(nil), defaultWSTopics...)
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		cleaned := strings.TrimSpace(strings.ToLower(part))
		if cleaned == "" {
			continue
		}
		if cleaned == "*" {
			return append([]string(nil), defaultWSTopics...)
		}
		if !isWSTopic(cleaned) {
			continue
		}
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		out = append(out, cleaned)
	}
	if len(out) == 0 {
		return append([]string(nil), defaultWSTopics...)
	}
	return out
}

func isWSTopic(topic string) bool {
	for _, candidate := range defaultWSTopics {
		if topic == candidate {
			return true
		}
	}
	return false
}

func normalizeWSLimit(raw string) int {
	cleaned := strings.TrimSpace(raw)
	if cleaned == "" {
		return DefaultWSResultLimit
	}
	parsed, err := strconv.Atoi(cleaned)
	if err != nil || parsed <= 0 {
		return DefaultWSResultLimit
	}
	if parsed > MaxWSResultLimit {
		return MaxWSResultLimit
	}
	return parsed
}
