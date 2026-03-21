package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"unified/core"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	var (
		configPath       string
		platform         string
		role             string
		operatorAddress  string
		operatorAlias    string
		repoRoot         string
		configSourcePath string
		bootnodesRaw     string
		rpcPort          int
		p2pPort          int
		sshPort          int
		backupDir        string
		backupRetention  int
	)

	cwd, _ := os.Getwd()

	flag.StringVar(&configPath, "config", "", "path to the pinned network config JSON")
	flag.StringVar(&platform, "platform", "linux", "target platform: linux or macos")
	flag.StringVar(&role, "role", "bootstrap", "node role: bootstrap or joiner")
	flag.StringVar(&operatorAddress, "operator-address", "", "operator UFI address for this node")
	flag.StringVar(&operatorAlias, "operator-alias", "mainnet-seed-1", "operator alias for this node")
	flag.StringVar(&repoRoot, "repo-root", cwd, "repo root to reference in generated commands")
	flag.StringVar(&configSourcePath, "config-source", "", "path to the pinned network config on the target host; defaults to the supplied --config path")
	flag.StringVar(&bootnodesRaw, "bootnodes", "", "optional comma-separated bootnode override for joiner nodes")
	flag.IntVar(&rpcPort, "rpc-port", 3337, "RPC port")
	flag.IntVar(&p2pPort, "p2p-port", 4001, "libp2p port")
	flag.IntVar(&sshPort, "ssh-port", 22, "SSH port for the Linux firewall helper")
	flag.StringVar(&backupDir, "backup-dir", "/var/backups/unified", "backup directory for Linux nodes")
	flag.IntVar(&backupRetention, "backup-retention", 14, "backup retention count for Linux nodes")
	flag.Parse()

	if strings.TrimSpace(configPath) == "" {
		return fmt.Errorf("config path is required")
	}
	if strings.TrimSpace(operatorAddress) == "" {
		return fmt.Errorf("operator address is required")
	}

	network, err := core.LoadNetworkConfig(configPath)
	if err != nil {
		return fmt.Errorf("load pinned config: %w", err)
	}

	platform = strings.ToLower(strings.TrimSpace(platform))
	role = strings.ToLower(strings.TrimSpace(role))
	if platform != "linux" && platform != "macos" {
		return fmt.Errorf("unsupported platform %q", platform)
	}
	if role != "bootstrap" && role != "joiner" {
		return fmt.Errorf("unsupported role %q", role)
	}

	bootnodes := strings.TrimSpace(bootnodesRaw)
	if bootnodes == "" {
		bootnodes = strings.Join(network.Bootnodes, ",")
	}
	if role == "bootstrap" {
		bootnodes = ""
	}
	if role == "joiner" && strings.TrimSpace(bootnodes) == "" {
		return fmt.Errorf("joiner role requires bootnodes either in the pinned config or via --bootnodes")
	}

	if strings.TrimSpace(configSourcePath) == "" {
		configSourcePath = configPath
	}

	repoRoot = filepath.Clean(repoRoot)
	configPath = filepath.Clean(configPath)
	configSourcePath = filepath.Clean(configSourcePath)

	fmt.Printf("# Pinned network config\n")
	fmt.Printf("go run ./scripts/verify_network_config --config %q\n", configPath)
	fmt.Println()

	switch platform {
	case "linux":
		printLinuxCommands(repoRoot, configSourcePath, role, operatorAddress, operatorAlias, bootnodes, rpcPort, p2pPort, sshPort, backupDir, backupRetention)
	case "macos":
		printMacOSCommands(repoRoot, configSourcePath, role, operatorAddress, operatorAlias, bootnodes, rpcPort)
	}

	return nil
}

func printLinuxCommands(repoRoot, configSourcePath, role, operatorAddress, operatorAlias, bootnodes string, rpcPort, p2pPort, sshPort int, backupDir string, backupRetention int) {
	fmt.Printf("# %s Linux node install\n", titleCase(role))
	fmt.Printf("cd %q\n", repoRoot)
	fmt.Printf("sudo make configure-firewall-linux P2P_PORT=%d SSH_PORT=%d RPCPORT=%d ALLOW_RPC_PUBLIC=0\n", p2pPort, sshPort, rpcPort)
	fmt.Printf("sudo UNIFIED_NETWORK_CONFIG_SOURCE=%q \\\n", configSourcePath)
	fmt.Printf("  UNIFIED_OPERATOR_ADDRESS=%q \\\n", operatorAddress)
	fmt.Printf("  UNIFIED_OPERATOR_ALIAS=%q \\\n", operatorAlias)
	if strings.TrimSpace(bootnodes) != "" {
		fmt.Printf("  UNIFIED_BOOTNODES=%q \\\n", bootnodes)
	}
	fmt.Printf("  ./scripts/ops/install_seed_node.sh --start --overwrite-env\n")
	fmt.Printf("sudo make install-backup-rotation DATADIR=/var/lib/unified BACKUP_DIR=%q BACKUP_RETENTION=%d\n", backupDir, backupRetention)
	fmt.Printf("curl -s http://127.0.0.1:%d/healthz\n", rpcPort)
	fmt.Printf("curl -s http://127.0.0.1:%d/readyz\n", rpcPort)
	fmt.Printf("go run ./scripts/verify_network_config --config %q --rpc-url http://127.0.0.1:%d\n", configSourcePath, rpcPort)
	if role == "bootstrap" {
		fmt.Println("sudo journalctl -u unified-seed-node -n 50 --no-pager | grep 'p2p listen address:'")
	} else {
		fmt.Printf("curl -s http://127.0.0.1:%d/p2p/peers\n", rpcPort)
	}
}

func printMacOSCommands(repoRoot, configSourcePath, role, operatorAddress, operatorAlias, bootnodes string, rpcPort int) {
	fmt.Printf("# %s macOS node install\n", titleCase(role))
	fmt.Printf("cd %q\n", repoRoot)
	fmt.Printf("sudo UNIFIED_NETWORK_CONFIG_SOURCE=%q \\\n", configSourcePath)
	fmt.Printf("  UNIFIED_OPERATOR_ADDRESS=%q \\\n", operatorAddress)
	fmt.Printf("  UNIFIED_OPERATOR_ALIAS=%q \\\n", operatorAlias)
	if strings.TrimSpace(bootnodes) != "" {
		fmt.Printf("  UNIFIED_BOOTNODES=%q \\\n", bootnodes)
	}
	fmt.Printf("  ./scripts/ops/install_seed_node_macos.sh --start --overwrite-env\n")
	fmt.Printf("curl -s http://127.0.0.1:%d/healthz\n", rpcPort)
	fmt.Printf("curl -s http://127.0.0.1:%d/readyz\n", rpcPort)
	fmt.Printf("go run ./scripts/verify_network_config --config %q --rpc-url http://127.0.0.1:%d\n", configSourcePath, rpcPort)
	if role == "bootstrap" {
		fmt.Println("grep 'p2p listen address:' /usr/local/var/log/unified/unified-seed-node.out.log")
	} else {
		fmt.Printf("curl -s http://127.0.0.1:%d/p2p/peers\n", rpcPort)
	}
}

func titleCase(value string) string {
	if value == "" {
		return value
	}
	return strings.ToUpper(value[:1]) + value[1:]
}
