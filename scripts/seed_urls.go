package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

type SeedTask struct {
	URL      string `json:"url"`
	Query    string `json:"query"`
	Bounty   string `json:"bounty"`
	Priority int    `json:"priority"`
}

func main() {
	rpcURL := flag.String("rpc", "http://127.0.0.1:8545/rpc", "Node RPC URL")
	filePath := flag.String("file", "urls.txt", "Path to urls.txt")
	defaultQuery := flag.String("q", "Genesis Index", "Default search term")
	flag.Parse()

	file, err := os.Open(*filePath)
	if err != nil {
		log.Fatalf("❌ Could not open %s: %v", *filePath, err)
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	fmt.Printf("🚀 Starting Genesis Seed: %d entries found.\n", len(lines))

	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.Split(line, ",")
		url := parts[0]
		query := *defaultQuery
		if len(parts) > 1 {
			query = parts[1]
		}

		task := SeedTask{
			URL:      url,
			Query:    query,
			Bounty:   "1.0",
			Priority: 10,
		}

		payload, _ := json.Marshal(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      i,
			"method":  "ufi_submitSearchTask",
			"params":  []interface{}{task},
		})

		resp, err := http.Post(*rpcURL, "application/json", bytes.NewBuffer(payload))
		if err != nil {
			fmt.Printf("⚠️  Failed %s: %v\n", url, err)
			continue
		}
		
		fmt.Printf("✅ [%d/%d] Seeded: %s\n", i+1, len(lines), url)
		resp.Body.Close()

		// Wait 1 second between injections to respect RPC rate limits
		// Wait 15 seconds every 32 URLs to respect Block Time
		if (i+1)%32 == 0 {
			fmt.Println("⏲️  Mempool batch full. Waiting for next block (15s)...")
			time.Sleep(15 * time.Second)
		} else {
			time.Sleep(500 * time.Millisecond)
		}
	}
	fmt.Println("🏁 Genesis Seeding Complete.")
}
