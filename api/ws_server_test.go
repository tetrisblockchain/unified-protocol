package api

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestWebSocketServerStreamsUpdatedSnapshots(t *testing.T) {
	t.Parallel()

	var blockNumber atomic.Uint64
	blockNumber.Store(1)

	server := httptest.NewServer(NewWebSocketServer(WebSocketConfig{
		PollInterval: 10 * time.Millisecond,
		LatestBlock: func() any {
			return map[string]any{
				"header": map[string]any{
					"number": blockNumber.Load(),
				},
			}
		},
	}))
	defer server.Close()

	wsURL := toWebSocketURL(server.URL + "/ws?topics=blocks")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer conn.Close()

	if envelope := readWSEnvelope(t, conn); envelope.Type != "hello" {
		t.Fatalf("first envelope type = %s, want hello", envelope.Type)
	}

	first := readWSEnvelope(t, conn)
	if first.Topic != "blocks" {
		t.Fatalf("first snapshot topic = %s, want blocks", first.Topic)
	}
	header, ok := first.Data.(map[string]any)["header"].(map[string]any)
	if !ok {
		t.Fatalf("first snapshot data = %#v, want block header", first.Data)
	}
	if got := header["number"]; got != float64(1) {
		t.Fatalf("first block number = %v, want 1", got)
	}

	blockNumber.Store(2)
	second := readWSEnvelope(t, conn)
	if second.Topic != "blocks" {
		t.Fatalf("second snapshot topic = %s, want blocks", second.Topic)
	}
	header, ok = second.Data.(map[string]any)["header"].(map[string]any)
	if !ok {
		t.Fatalf("second snapshot data = %#v, want block header", second.Data)
	}
	if got := header["number"]; got != float64(2) {
		t.Fatalf("second block number = %v, want 2", got)
	}
}

func readWSEnvelope(t *testing.T, conn *websocket.Conn) wsEnvelope {
	t.Helper()

	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline returned error: %v", err)
	}
	var envelope wsEnvelope
	if err := conn.ReadJSON(&envelope); err != nil {
		t.Fatalf("ReadJSON returned error: %v", err)
	}
	return envelope
}

func toWebSocketURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return raw
	}
	switch parsed.Scheme {
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	}
	return parsed.String()
}
