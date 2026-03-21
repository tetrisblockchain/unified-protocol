package main

import (
	"bytes"
	"crypto/ecdsa" // Added
	"encoding/hex"   // Added
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/gorilla/websocket"
)

// --- CONFIGURATION ---
const (
	MinerRPC   = "http://127.0.0.1:18545/rpc"
	MinerWS    = "ws://127.0.0.1:18545/ws"
	PrivateKey = "a7c996df71740c73284993607862e05db716c18684fed2d01cec05a531447c30" 
	MyAddress  = "UFIn84xgEU1xGTcZWojs8FceAz9PDCMV9RXZ"
)

type RPCRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
	ID      int           `json:"id"`
}

type SeedEntry struct {
	URL   string
	Query string
}

func main() {
	// 1. Connect to the WebSocket Success Listener
	conn, _, err := websocket.DefaultDialer.Dial(MinerWS, nil)
	if err != nil {
		log.Printf("⚠️  WS Listener Offline: %v", err)
	} else {
		defer conn.Close()
		fmt.Println("📡 Success Listener: CONNECTED.")
		go listenForIndexSuccess(conn)
	}

	// 2. Setup Signer
	privKey, err := crypto.HexToECDSA(strings.TrimPrefix(PrivateKey, "0x"))
	if err != nil {
		log.Fatalf("❌ Invalid Private Key: %v", err)
	}

	urls := generateMassiveList()
	total := len(urls)

	fmt.Printf("🚀 Starting Genesis Seed of %d URLs...\n", total)

	// 3. Execution Loop
	for i, entry := range urls {
		nonce := uint64(time.Now().UnixNano() + int64(i))
		
		// This now passes the privKey for real signing
		payload := createSignedTask(entry.URL, entry.Query, nonce, privKey)

		err := sendToMiner(payload, i)
		
		percent := float64(i+1) / float64(total) * 100
		if err == nil {
			fmt.Printf("\r📤 Progress: [%-20s] %.1f%% | Signed & Sent: %s", 
				strings.Repeat("=", int(percent/5)), percent, entry.URL)
		}

		time.Sleep(500 * time.Millisecond)
	}

	fmt.Println("\n🏁 All tasks sent. Waiting for network confirmations...")
	select {} 
}

// --- HELPER FUNCTIONS ---

func generateMassiveList() []SeedEntry {
	domains := []string{
		"nature.com", "nasa.gov", "mit.edu", "arxiv.org", "github.com", 
		"who.int", "harvard.edu", "stanford.edu", "wikipedia.org", "nih.gov",
	}
	paths := []string{"", "/news", "/research", "/docs", "/data", "/about"}
	var list []SeedEntry
	for _, d := range domains {
		for _, p := range paths {
			list = append(list, SeedEntry{URL: "https://" + d + p, Query: "Genesis Knowledge Index"})
		}
	}
	return list
}

func createSignedTask(url, query string, nonce uint64, privKey *ecdsa.PrivateKey) map[string]interface{} {
	// 1. Transaction Data
	txData := map[string]interface{}{
		"type":  2,
		"from":  MyAddress,
		"to":    "0x101",
		"value": "1000000000000000000",
		"nonce": nonce,
	}

	// 2. Sign the transaction hash
	data, _ := json.Marshal(txData)
	hash := crypto.Keccak256Hash(data)
	signature, _ := crypto.Sign(hash.Bytes(), privKey)
	sigHex := "0x" + hex.EncodeToString(signature)

	return map[string]interface{}{
		"transaction": map[string]interface{}{
			"type":      2,
			"from":      MyAddress,
			"to":        "0x101",
			"value":     "1000000000000000000",
			"nonce":     nonce,
			"signature": sigHex, 
		},
		"task": map[string]interface{}{
			"url":      url,
			"query":    query,
			"priority": 10,
		},
	}
}

func sendToMiner(params map[string]interface{}, id int) error {
	reqBody, _ := json.Marshal(RPCRequest{
		JSONRPC: "2.0",
		Method:  "ufi_submitSearchTask",
		Params:  []interface{}{params},
		ID:      id,
	})

	resp, err := http.Post(MinerRPC, "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func listenForIndexSuccess(conn *websocket.Conn) {
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if strings.Contains(string(message), "search_confirmed") {
			fmt.Printf("\n✨ [NETWORK CONFIRMED] Knowledge Indexed!")
		}
	}
}
