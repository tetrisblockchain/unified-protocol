GO ?= go
SOLC ?= npx --yes solc@0.8.24

BUILD_DIR ?= build
NODE_BIN := $(BUILD_DIR)/unified-node
CLI_BIN := $(BUILD_DIR)/unified-cli

DATADIR ?= ./data/local
RPCHOST ?= 127.0.0.1
RPCPORT ?= 8545
P2P_LISTEN ?= /ip4/0.0.0.0/tcp/0
BOOTNODES ?=
GENESIS_ADDRESS ?= UFI_LOCAL_OPERATOR
OPERATOR ?= UFI_LOCAL_OPERATOR
OPERATOR_ALIAS ?= local-operator
OPERATOR_VOTING_POWER ?= 5000
CIRCULATING_SUPPLY ?= 1000000

ARCHITECT_KEY ?=
GENESIS_URL ?=
GENESIS_QUERY ?=
URLS_FILE ?=
SEED_QUERY ?= initial web seed
SEED_BASE_BOUNTY ?= 1.0
SEED_DIFFICULTY ?= 8
SEED_DATA_VOLUME_BYTES ?= 1024
SEED_BATCH_SIZE ?= 32
BACKUP_OUTPUT ?=
BACKUP_ARCHIVE ?=
RESTORE_TARGET ?=
PID_FILE ?=
ROLL_LOG ?= ./logs/unified-node-rollout.log

.PHONY: setup tidy fmt test build build-node build-cli run-node run-mine genesis bootstrap-architect seed-urls check-node backup-datadir restore-datadir rollout-node solc-uns smoke-health smoke-rpc clean

setup:
	./setup.sh

tidy:
	$(GO) mod tidy

fmt:
	$(GO) fmt ./...

test:
	$(GO) test ./...

build: build-node build-cli

build-node:
	mkdir -p $(BUILD_DIR)
	$(GO) build -o $(NODE_BIN) ./cmd/unified-node

build-cli:
	mkdir -p $(BUILD_DIR)
	$(GO) build -o $(CLI_BIN) ./cmd/unified-cli

run-node:
	$(GO) run ./cmd/unified-node \
		--datadir "$(DATADIR)" \
		--rpchost "$(RPCHOST)" \
		--rpcport "$(RPCPORT)" \
		--p2p-listen "$(P2P_LISTEN)" \
		--bootnodes "$(BOOTNODES)" \
		--genesis-address "$(GENESIS_ADDRESS)" \
		--operator "$(OPERATOR)" \
		--operator-alias "$(OPERATOR_ALIAS)" \
		--operator-voting-power "$(OPERATOR_VOTING_POWER)" \
		--circulating-supply "$(CIRCULATING_SUPPLY)"

run-mine:
	$(GO) run ./cmd/unified-node \
		--mine \
		--datadir "$(DATADIR)" \
		--rpchost "$(RPCHOST)" \
		--rpcport "$(RPCPORT)" \
		--p2p-listen "$(P2P_LISTEN)" \
		--bootnodes "$(BOOTNODES)" \
		--genesis-address "$(GENESIS_ADDRESS)" \
		--operator "$(OPERATOR)" \
		--operator-alias "$(OPERATOR_ALIAS)" \
		--operator-voting-power "$(OPERATOR_VOTING_POWER)" \
		--circulating-supply "$(CIRCULATING_SUPPLY)"

genesis:
	test -n "$(ARCHITECT_KEY)" || (echo "ARCHITECT_KEY is required" && exit 1)
	UFI_ARCHITECT_KEY="$(ARCHITECT_KEY)" \
	UFI_RPC_URL="http://$(RPCHOST):$(RPCPORT)" \
	UFI_GENESIS_URL="$(GENESIS_URL)" \
	UFI_GENESIS_QUERY="$(GENESIS_QUERY)" \
	$(GO) run ./scripts/genesis_tx.go

bootstrap-architect:
	test -n "$(ARCHITECT_KEY)" || (echo "ARCHITECT_KEY is required" && exit 1)
	UFI_ARCHITECT_KEY="$(ARCHITECT_KEY)" \
	UFI_RPC_HOST="$(RPCHOST)" \
	UFI_RPC_PORT="$(RPCPORT)" \
	UFI_DATADIR="$(DATADIR)" \
	UFI_P2P_LISTEN="$(P2P_LISTEN)" \
	UFI_BOOTNODES="$(BOOTNODES)" \
	UFI_OPERATOR_ALIAS="$(OPERATOR_ALIAS)" \
	UFI_OPERATOR_VOTING_POWER="$(OPERATOR_VOTING_POWER)" \
	UFI_CIRCULATING_SUPPLY="$(CIRCULATING_SUPPLY)" \
	UFI_GENESIS_URL="$(GENESIS_URL)" \
	UFI_GENESIS_QUERY="$(GENESIS_QUERY)" \
	./scripts/bootstrap_architect.sh

seed-urls:
	test -n "$(ARCHITECT_KEY)" || (echo "ARCHITECT_KEY is required" && exit 1)
	test -n "$(URLS_FILE)" || (echo "URLS_FILE is required" && exit 1)
	UFI_ARCHITECT_KEY="$(ARCHITECT_KEY)" \
	UFI_RPC_URL="http://$(RPCHOST):$(RPCPORT)" \
	$(GO) run ./scripts/seed_urls \
		--file "$(URLS_FILE)" \
		--query "$(SEED_QUERY)" \
		--base-bounty "$(SEED_BASE_BOUNTY)" \
		--difficulty "$(SEED_DIFFICULTY)" \
		--data-volume-bytes "$(SEED_DATA_VOLUME_BYTES)" \
		--batch-size "$(SEED_BATCH_SIZE)"

check-node:
	UFI_RPC_URL="http://$(RPCHOST):$(RPCPORT)" \
	./scripts/ops/check_node.sh

backup-datadir:
	test -n "$(DATADIR)" || (echo "DATADIR is required" && exit 1)
	./scripts/ops/backup_datadir.sh "$(DATADIR)" "$(BACKUP_OUTPUT)"

restore-datadir:
	test -n "$(BACKUP_ARCHIVE)" || (echo "BACKUP_ARCHIVE is required" && exit 1)
	test -n "$(RESTORE_TARGET)" || (echo "RESTORE_TARGET is required" && exit 1)
	./scripts/ops/restore_datadir.sh "$(BACKUP_ARCHIVE)" "$(RESTORE_TARGET)"

rollout-node:
	UFI_DATADIR="$(DATADIR)" \
	UFI_PID_FILE="$(PID_FILE)" \
	UFI_LOG_FILE="$(ROLL_LOG)" \
	./scripts/ops/rollout_node.sh \
		--datadir "$(DATADIR)" \
		--rpchost "$(RPCHOST)" \
		--rpcport "$(RPCPORT)" \
		--p2p-listen "$(P2P_LISTEN)" \
		--bootnodes "$(BOOTNODES)" \
		--genesis-address "$(GENESIS_ADDRESS)" \
		--operator "$(OPERATOR)" \
		--operator-alias "$(OPERATOR_ALIAS)" \
		--operator-voting-power "$(OPERATOR_VOTING_POWER)" \
		--circulating-supply "$(CIRCULATING_SUPPLY)"

solc-uns:
	mkdir -p $(BUILD_DIR)
	$(SOLC) --bin --abi -o $(BUILD_DIR) contracts/UNS.sol

smoke-health:
	curl -fsS "http://$(RPCHOST):$(RPCPORT)/healthz"

smoke-rpc:
	curl -fsS -X POST "http://$(RPCHOST):$(RPCPORT)/rpc" \
		-H 'Content-Type: application/json' \
		-d '{"jsonrpc":"2.0","id":1,"method":"ufi_getBlockByNumber","params":{"number":"latest"}}'

clean:
	rm -rf $(BUILD_DIR)
