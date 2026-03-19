GO ?= go
NPM ?= npm
SOLC ?= npx --yes solc@0.8.24

BUILD_DIR ?= build
NODE_BIN := $(BUILD_DIR)/unified-node
CLI_BIN := $(BUILD_DIR)/unified-cli
DESKTOP_DIR := web3/desktop

DATADIR ?= ./data/local
RPCHOST ?= 127.0.0.1
RPCPORT ?= 8545
P2P_LISTEN ?= /ip4/0.0.0.0/tcp/0
P2P_PORT ?= 4001
BOOTNODES ?=
NETWORK_CONFIG ?=
NETWORK_NAME ?=
CHAIN_ID ?=
ARCHITECT_ADDRESS ?=
GENESIS_ADDRESS ?= UFI_LOCAL_OPERATOR
OPERATOR ?= UFI_LOCAL_OPERATOR
OPERATOR_ALIAS ?= local-operator
OPERATOR_VOTING_POWER ?= 5000
CIRCULATING_SUPPLY ?= 1000000

ARCHITECT_KEY ?=
SEED_KEY ?=
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
BACKUP_DIR ?= /var/backups/unified
BACKUP_RETENTION ?= 7
BACKUP_ON_CALENDAR ?= daily
BACKUP_RANDOM_DELAY ?= 15m
SSH_PORT ?= 22
ALLOW_RPC_PUBLIC ?= 0
PLATFORM ?= linux
ROLE ?= bootstrap
VERIFY_RPC_URL ?=

.PHONY: setup tidy fmt test build build-node build-cli desktop-install desktop-dev desktop-build desktop-start install-go-linux install-seed-node install-seed-node-macos install-backup-rotation configure-firewall-linux generate-operator generate-network-config verify-network-config print-cutover-commands run-node run-mine genesis bootstrap-architect seed-urls check-node backup-datadir restore-datadir rollout-node solc-uns smoke-health smoke-rpc clean

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

desktop-install:
	cd $(DESKTOP_DIR) && $(NPM) install

desktop-dev:
	cd $(DESKTOP_DIR) && $(NPM) run dev

desktop-build:
	cd $(DESKTOP_DIR) && $(NPM) run build

desktop-start: desktop-build
	cd $(DESKTOP_DIR) && $(NPM) run start

install-go-linux:
	./scripts/ops/install_go_linux.sh

install-seed-node:
	./scripts/ops/install_seed_node.sh

install-seed-node-macos:
	./scripts/ops/install_seed_node_macos.sh

install-backup-rotation:
	UNIFIED_DATA_DIR="$(DATADIR)" \
	UNIFIED_BACKUP_DIR="$(BACKUP_DIR)" \
	UNIFIED_BACKUP_RETENTION="$(BACKUP_RETENTION)" \
	UNIFIED_BACKUP_ON_CALENDAR="$(BACKUP_ON_CALENDAR)" \
	UNIFIED_BACKUP_RANDOM_DELAY="$(BACKUP_RANDOM_DELAY)" \
	./scripts/ops/install_backup_rotation.sh

configure-firewall-linux:
	UNIFIED_P2P_PORT="$(P2P_PORT)" \
	UNIFIED_SSH_PORT="$(SSH_PORT)" \
	UNIFIED_RPC_PORT="$(RPCPORT)" \
	UNIFIED_ALLOW_RPC_PUBLIC="$(ALLOW_RPC_PUBLIC)" \
	./scripts/ops/configure_firewall_linux.sh

generate-operator:
	$(GO) run ./scripts/generate_operator_identity

generate-network-config:
	$(GO) run ./scripts/generate_network_config \
		--output "$(NETWORK_CONFIG)" \
		--name "$(NETWORK_NAME)" \
		--chain-id "$(CHAIN_ID)" \
		--genesis-address "$(GENESIS_ADDRESS)" \
		--architect-address "$(ARCHITECT_ADDRESS)" \
		--circulating-supply "$(CIRCULATING_SUPPLY)" \
		--bootnodes "$(BOOTNODES)"

verify-network-config:
	test -n "$(NETWORK_CONFIG)" || (echo "NETWORK_CONFIG is required" && exit 1)
	$(GO) run ./scripts/verify_network_config \
		--config "$(NETWORK_CONFIG)" \
		$(if $(VERIFY_RPC_URL),--rpc-url "$(VERIFY_RPC_URL)",)

print-cutover-commands:
	test -n "$(NETWORK_CONFIG)" || (echo "NETWORK_CONFIG is required" && exit 1)
	test -n "$(OPERATOR)" || (echo "OPERATOR is required" && exit 1)
	$(GO) run ./scripts/print_cutover_commands \
		--config "$(NETWORK_CONFIG)" \
		--platform "$(PLATFORM)" \
		--role "$(ROLE)" \
		--operator-address "$(OPERATOR)" \
		--operator-alias "$(OPERATOR_ALIAS)" \
		--bootnodes "$(BOOTNODES)" \
		--rpc-port "$(RPCPORT)" \
		--p2p-port "$(P2P_PORT)" \
		--ssh-port "$(SSH_PORT)" \
		--backup-dir "$(BACKUP_DIR)" \
		--backup-retention "$(BACKUP_RETENTION)"

run-node:
	$(GO) run ./cmd/unified-node \
		--network-config "$(NETWORK_CONFIG)" \
		--network-name "$(NETWORK_NAME)" \
		--chain-id "$(CHAIN_ID)" \
		--datadir "$(DATADIR)" \
		--rpchost "$(RPCHOST)" \
		--rpcport "$(RPCPORT)" \
		--p2p-listen "$(P2P_LISTEN)" \
		--bootnodes "$(BOOTNODES)" \
		--genesis-address "$(GENESIS_ADDRESS)" \
		--architect-address "$(ARCHITECT_ADDRESS)" \
		--operator "$(OPERATOR)" \
		--operator-alias "$(OPERATOR_ALIAS)" \
		--operator-voting-power "$(OPERATOR_VOTING_POWER)" \
		--circulating-supply "$(CIRCULATING_SUPPLY)"

run-mine:
	$(GO) run ./cmd/unified-node \
		--mine \
		--network-config "$(NETWORK_CONFIG)" \
		--network-name "$(NETWORK_NAME)" \
		--chain-id "$(CHAIN_ID)" \
		--datadir "$(DATADIR)" \
		--rpchost "$(RPCHOST)" \
		--rpcport "$(RPCPORT)" \
		--p2p-listen "$(P2P_LISTEN)" \
		--bootnodes "$(BOOTNODES)" \
		--genesis-address "$(GENESIS_ADDRESS)" \
		--architect-address "$(ARCHITECT_ADDRESS)" \
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
	$(GO) run ./scripts/genesis_tx

bootstrap-architect:
	test -n "$(ARCHITECT_KEY)" || (echo "ARCHITECT_KEY is required" && exit 1)
	UFI_ARCHITECT_KEY="$(ARCHITECT_KEY)" \
	UFI_RPC_HOST="$(RPCHOST)" \
	UFI_RPC_PORT="$(RPCPORT)" \
	UFI_DATADIR="$(DATADIR)" \
	UFI_NETWORK_CONFIG="$(NETWORK_CONFIG)" \
	UFI_P2P_LISTEN="$(P2P_LISTEN)" \
	UFI_BOOTNODES="$(BOOTNODES)" \
	UFI_OPERATOR_ALIAS="$(OPERATOR_ALIAS)" \
	UFI_OPERATOR_VOTING_POWER="$(OPERATOR_VOTING_POWER)" \
	UFI_CIRCULATING_SUPPLY="$(CIRCULATING_SUPPLY)" \
	UFI_GENESIS_URL="$(GENESIS_URL)" \
	UFI_GENESIS_QUERY="$(GENESIS_QUERY)" \
	./scripts/bootstrap_architect.sh

seed-urls:
	test -n "$(SEED_KEY)$(ARCHITECT_KEY)" || (echo "SEED_KEY or ARCHITECT_KEY is required" && exit 1)
	test -n "$(URLS_FILE)" || (echo "URLS_FILE is required" && exit 1)
	UFI_SEED_KEY="$(if $(SEED_KEY),$(SEED_KEY),$(ARCHITECT_KEY))" \
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
		--network-config "$(NETWORK_CONFIG)" \
		--network-name "$(NETWORK_NAME)" \
		--chain-id "$(CHAIN_ID)" \
		--datadir "$(DATADIR)" \
		--rpchost "$(RPCHOST)" \
		--rpcport "$(RPCPORT)" \
		--p2p-listen "$(P2P_LISTEN)" \
		--bootnodes "$(BOOTNODES)" \
		--genesis-address "$(GENESIS_ADDRESS)" \
		--architect-address "$(ARCHITECT_ADDRESS)" \
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
	rm -rf $(DESKTOP_DIR)/dist
