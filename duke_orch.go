package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"gopkg.in/yaml.v3"
)

type Node struct {
	ID      string `yaml:"id"`
	Address string `yaml:"address"`
	ApiAt   string `yaml:"api_at"`
}

type NonSeedNodes struct {
	FirstPeer *string `yaml:"first_peer"`
	Nodes     []Node  `yaml:"nodes"`
}

type Control struct {
	Version           string       `yaml:"version"`
	DukeExecutable    string       `yaml:"duke_executable"`
	TotalNodes        int          `yaml:"total_nodes"`
	UseDocker         bool         `yaml:"use_docker"`
	DockerImage       string       `yaml:"docker_image"`
	SeedNode          Node         `yaml:"seed_node"`
	NonSeedNodes      NonSeedNodes `yaml:"non_seed_nodes"`
	LoggingFile       string       `yaml:"logging_file"`
	ReplicationFactor string       `yaml:"replication_factor"`
}

// ----- Package-level variables for logging -----
var (
	logMutex sync.Mutex
	logFile  *os.File
)

func main() {
	var controlFile string
	flag.StringVar(&controlFile, "control-file", "", "path to control file")
	flag.StringVar(&controlFile, "cf", "", "path to control file (shorthand)")
	flag.Parse()

	if controlFile == "" {
		panic("Control file not provided! Do ./duke_orch -help.")
	}

	data, err := os.ReadFile(controlFile)
	if err != nil {
		panic(err)
	}

	var control Control
	err = yaml.Unmarshal(data, &control)
	if err != nil {
		panic(err)
	}

	// Validate Docker Configuration
	if control.UseDocker && control.DockerImage == "" {
		panic("Error: 'docker_image' must be provided in the control file when 'use_docker' is true.")
	}

	if control.LoggingFile != "" {
		file, err := os.OpenFile(control.LoggingFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			panic(fmt.Sprintf("failed to open log file: %v", err))
		}
		logFile = file
		defer file.Close()
	}

	// Create Docker network if Docker mode is enabled
	if control.UseDocker {
		ensureDockerNetwork("duke-net")
	}

	// Build a map of all nodes (seed + non‑seed) for easy lookup by ID
	allNodes := map[string]*Node{
		control.SeedNode.ID: &control.SeedNode,
	}
	for i := range control.NonSeedNodes.Nodes {
		node := &control.NonSeedNodes.Nodes[i]
		allNodes[node.ID] = node
	}

	// Determine the peer for all non‑seed nodes.
	peerID := control.SeedNode.ID
	if control.NonSeedNodes.FirstPeer != nil && *control.NonSeedNodes.FirstPeer != "" {
		peerID = *control.NonSeedNodes.FirstPeer
	}
	peerNode, ok := allNodes[peerID]
	if !ok {
		panic(fmt.Sprintf("peer node with ID %q not found", peerID))
	}

	// Prepare commands for all nodes (seed + non‑seed)
	var cmds []*exec.Cmd

	// Seed node
	seedCmd := commandForSeedNode(
		control.DukeExecutable,
		control.DockerImage,
		control.SeedNode.ID,
		control.SeedNode.Address,
		control.SeedNode.ApiAt,
		control.ReplicationFactor,
		control.UseDocker,
	)
	cmds = append(cmds, seedCmd)

	// Non‑seed nodes
	for _, node := range control.NonSeedNodes.Nodes {
		cmd := commandForNonSeedNode(
			control.DukeExecutable,
			control.DockerImage,
			node.ID,
			node.Address,
			peerNode.Address,
			peerNode.ID,
			node.ApiAt,
			control.ReplicationFactor,
			control.UseDocker,
		)
		cmds = append(cmds, cmd)
	}

	// Run all nodes with logging
	runNodes(cmds)
}

// ensureDockerNetwork creates the specified bridge network if it doesn't already exist.
func ensureDockerNetwork(network string) {
	cmd := exec.Command("docker", "network", "inspect", network)
	if err := cmd.Run(); err != nil {
		createCmd := exec.Command("docker", "network", "create", network)
		if err := createCmd.Run(); err != nil {
			panic(fmt.Sprintf("failed to create docker network %s: %v", network, err))
		}
	}
}

// extractPort gets just the port number from a "host:port" address string.
func extractPort(addr string) string {
	parts := strings.Split(addr, ":")
	if len(parts) > 1 {
		return parts[len(parts)-1]
	}
	return addr
}

// dockerAddress rewrites a local address to a Docker container address (e.g. duke-a:8000).
func dockerAddress(nodeID, localAddr string) string {
	port := extractPort(localAddr)
	return fmt.Sprintf("duke-%s:%s", nodeID, port)
}

// dockerSeedCommand builds the exec.Cmd for the seed node using Docker.
func dockerSeedCommand(image, selfNodeID, selfAddr, apiAt, replicationFactor string) *exec.Cmd {
	containerName := fmt.Sprintf("duke-%s", selfNodeID)
	hostApiPort := extractPort(apiAt)
	dockerSelfAddr := dockerAddress(selfNodeID, selfAddr)

	return exec.Command("docker", "run", "--rm",
		"--name", containerName,
		"--network", "duke-net",
		"-p", fmt.Sprintf("%s:9000", hostApiPort),
		image,
		"-self-node-id", selfNodeID,
		"-self-addr", dockerSelfAddr,
		"-seed-node=true",
		"-api-at", "0.0.0.0:9000",
		"-replication-factor", replicationFactor,
	)
}

// commandForSeedNode builds the exec.Cmd for the seed node.
func commandForSeedNode(executable, dockerImage, selfNodeID, selfAddr, apiAt, replicationFactor string, useDocker bool) *exec.Cmd {
	if useDocker {
		return dockerSeedCommand(dockerImage, selfNodeID, selfAddr, apiAt, replicationFactor)
	}
	return exec.Command(executable,
		"-self-node-id", selfNodeID,
		"-self-addr", selfAddr,
		"-seed-node=true",
		"-api-at", apiAt,
		"-replication-factor", replicationFactor,
	)
}

// dockerNonSeedCommand builds the exec.Cmd for a non-seed node using Docker.
func dockerNonSeedCommand(image, selfNodeID, selfAddr, peerAddr, peerNodeID, apiAt, replicationFactor string) *exec.Cmd {
	containerName := fmt.Sprintf("duke-%s", selfNodeID)
	hostApiPort := extractPort(apiAt)
	dockerSelfAddr := dockerAddress(selfNodeID, selfAddr)
	dockerPeerAddr := dockerAddress(peerNodeID, peerAddr)

	return exec.Command("docker", "run", "--rm",
		"--name", containerName,
		"--network", "duke-net",
		"-p", fmt.Sprintf("%s:9000", hostApiPort),
		image,
		"-self-node-id", selfNodeID,
		"-self-addr", dockerSelfAddr,
		"-peer-addr", dockerPeerAddr,
		"-peer-node-id", peerNodeID,
		"-api-at", "0.0.0.0:9000",
		"-replication-factor", replicationFactor,
	)
}

// commandForNonSeedNode builds the exec.Cmd for a non‑seed node.
func commandForNonSeedNode(
	executable,
	dockerImage,
	selfNodeID,
	selfAddr,
	peerAddr,
	peerNodeID,
	apiAt string,
	replicationFactor string,
	useDocker bool,
) *exec.Cmd {
	if useDocker {
		return dockerNonSeedCommand(dockerImage, selfNodeID, selfAddr, peerAddr, peerNodeID, apiAt, replicationFactor)
	}
	return exec.Command(executable,
		"-self-node-id", selfNodeID,
		"-self-addr", selfAddr,
		"-peer-addr", peerAddr,
		"-peer-node-id", peerNodeID,
		"-api-at", apiAt,
		"-replication-factor", replicationFactor,
	)
}

// stopDockerContainers cleanly stops any containers started by the orchestrator.
func stopDockerContainers(cmds []*exec.Cmd) {
	for _, cmd := range cmds {
		if len(cmd.Args) > 0 && cmd.Args[0] == "docker" {
			nodeID := getNodeID(cmd.Args)
			if nodeID != "unknown" {
				containerName := fmt.Sprintf("duke-%s", nodeID)
				stopCmd := exec.Command("docker", "stop", containerName)
				_ = stopCmd.Run() // Ignore errors
			}
		}
	}
}

// logOutput writes a line to stdout (with prefix) and to the log file (if open).
func logOutput(nodeID, line string) {
	formatted := fmt.Sprintf("[%s] %s\n", nodeID, line)

	fmt.Print(formatted)

	if logFile != nil {
		logMutex.Lock()
		defer logMutex.Unlock()
		_, _ = logFile.WriteString(formatted)
	}
}

func runNodes(cmds []*exec.Cmd) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		logOutput("orchestrator", "Received interrupt, shutting down all nodes...")
		stopDockerContainers(cmds)
		cancel()
	}()

	var wg sync.WaitGroup

	for _, cmd := range cmds {
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			panic(fmt.Sprintf("failed to get stdout pipe: %v", err))
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			panic(fmt.Sprintf("failed to get stderr pipe: %v", err))
		}

		if err := cmd.Start(); err != nil {
			panic(fmt.Sprintf("failed to start node %v: %v", cmd.Args, err))
		}

		nodeID := getNodeID(cmd.Args)
		pid := cmd.Process.Pid
		logOutput(nodeID, fmt.Sprintf("started: %s (pid=%d)", cmd.String(), pid))

		wg.Add(1)
		go func(cmd *exec.Cmd, stdout io.ReadCloser) {
			defer wg.Done()
			scanner := bufio.NewScanner(stdout)
			nodeID := getNodeID(cmd.Args)
			for scanner.Scan() {
				logOutput(nodeID, scanner.Text())
			}
		}(cmd, stdout)

		wg.Add(1)
		go func(cmd *exec.Cmd, stderr io.ReadCloser) {
			defer wg.Done()
			scanner := bufio.NewScanner(stderr)
			nodeID := getNodeID(cmd.Args)
			for scanner.Scan() {
				logOutput(nodeID, scanner.Text())
			}
		}(cmd, stderr)

		go func(cmd *exec.Cmd) {
			err := cmd.Wait()
			if err != nil {
				select {
				case <-ctx.Done():
				default:
					logOutput(getNodeID(cmd.Args), "process exited with error: "+err.Error())
				}
			}
		}(cmd)
	}

	wg.Wait()
	logOutput("orchestrator", "All nodes have stopped.")
}

// getNodeID extracts the node ID from the command arguments.
func getNodeID(args []string) string {
	for i, arg := range args {
		if arg == "-self-node-id" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return "unknown"
}
