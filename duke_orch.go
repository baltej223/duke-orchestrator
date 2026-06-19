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
	Version        string       `yaml:"version"`
	DukeExecutable string       `yaml:"duke_executable"`
	TotalNodes     int          `yaml:"total_nodes"`
	SeedNode       Node         `yaml:"seed_node"`
	NonSeedNodes   NonSeedNodes `yaml:"non_seed_nodes"`
	LoggingFile    string       `yaml:"logging_file"` // new field
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

	if control.LoggingFile != "" {
		file, err := os.OpenFile(control.LoggingFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			panic(fmt.Sprintf("failed to open log file: %v", err))
		}
		logFile = file
		defer file.Close()
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
	// If first_peer is provided, use that ID; otherwise use the seed node's ID.
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
	seedCmd := commandForSeedNode(control.DukeExecutable, control.SeedNode.ID, control.SeedNode.Address, control.SeedNode.ApiAt)
	cmds = append(cmds, seedCmd)

	// Non‑seed nodes
	for _, node := range control.NonSeedNodes.Nodes {
		cmd := commandForNonSeedNode(control.DukeExecutable, node.ID, node.Address, peerNode.Address, peerNode.ID, node.ApiAt)
		cmds = append(cmds, cmd)
	}

	// Run all nodes with logging
	runNodes(cmds)
}

// commandForSeedNode builds the exec.Cmd for the seed node.
func commandForSeedNode(executable, selfNodeID, selfAddr, apiAt string) *exec.Cmd {
	return exec.Command(executable,
		"-self-node-id", selfNodeID,
		"-self-addr", selfAddr,
		"-seed-node=true",
		"-api-at", apiAt,
	)
}

// commandForNonSeedNode builds the exec.Cmd for a non‑seed node.
func commandForNonSeedNode(executable, selfNodeID, selfAddr, peerAddr, peerNodeID, apiAt string) *exec.Cmd {
	return exec.Command(executable,
		"-self-node-id", selfNodeID,
		"-self-addr", selfAddr,
		"-peer-addr", peerAddr,
		"-peer-node-id", peerNodeID,
		"-api-at", apiAt,
	)
}

// logOutput writes a line to stdout (with prefix) and to the log file (if open).
func logOutput(nodeID, line string) {
	// Format the line with the node prefix
	formatted := fmt.Sprintf("[%s] %s\n", nodeID, line)

	// Write to stdout
	fmt.Print(formatted)

	// Write to file if configured
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
		logOutput(nodeID, "started: "+cmd.String())

		// stdout scanner
		wg.Add(1)
		go func(cmd *exec.Cmd, stdout io.ReadCloser) {
			defer wg.Done()
			scanner := bufio.NewScanner(stdout)
			nodeID := getNodeID(cmd.Args)
			for scanner.Scan() {
				logOutput(nodeID, scanner.Text())
			}
		}(cmd, stdout)

		// stderr scanner
		wg.Add(1)
		go func(cmd *exec.Cmd, stderr io.ReadCloser) {
			defer wg.Done()
			scanner := bufio.NewScanner(stderr)
			nodeID := getNodeID(cmd.Args)
			for scanner.Scan() {
				logOutput(nodeID, scanner.Text())
			}
		}(cmd, stderr)

		// wait for process exit
		go func(cmd *exec.Cmd) {
			err := cmd.Wait()
			if err != nil {
				select {
				case <-ctx.Done():
					// normal termination
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
// Assumes that "-self-node-id" is present and followed by the ID.
func getNodeID(args []string) string {
	for i, arg := range args {
		if arg == "-self-node-id" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return "unknown"
}
