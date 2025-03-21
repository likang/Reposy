package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

const (
	socketPath = "/tmp/reposy.sock"
)

type Message struct {
	Command string `json:"command"`
	Args    string `json:"args,omitempty"`
}

type Response struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Data    string `json:"data,omitempty"`
}

func main() {
	var rootCmd = &cobra.Command{
		Use:   "reposy",
		Short: "Reposy syncs local repository folders with S3",
		Long:  `Reposy is a CLI tool that syncs local repository folders with S3 buckets based on configuration.`,
	}

	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show sync status of repositories",
		Run: func(cmd *cobra.Command, args []string) {
			if !isDaemonRunning() {
				fmt.Println("Reposy sync service is not running. Please run 'reposy start' first")
				return
			}
			resp := sendCommand("status", "")
			fmt.Println(resp.Message)
			if resp.Data != "" {
				fmt.Println(resp.Data)
			}
		},
	}

	reloadCmd := &cobra.Command{
		Use:   "reload",
		Short: "Reload configuration",
		Run: func(cmd *cobra.Command, args []string) {
			if !isDaemonRunning() {
				fmt.Println("Reposy sync service is not running. Please run 'reposy start' first")
				return
			}
			resp := sendCommand("reload", "")
			fmt.Println(resp.Message)
		},
	}

	startCmd := &cobra.Command{
		Use:   "start",
		Short: "Start the sync service if not running",
		Run: func(cmd *cobra.Command, args []string) {
			if isDaemonRunning() {
				fmt.Println("Reposy sync service is already running")
				return
			}
			startDaemon()
			fmt.Println("Reposy sync service started")
		},
	}

	stopCmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the sync service if running",
		Run: func(cmd *cobra.Command, args []string) {
			if !isDaemonRunning() {
				fmt.Println("Reposy sync service is not running")
				return
			}
			resp := sendCommand("shutdown", "")
			fmt.Println(resp.Message)
		},
	}

	rootCmd.AddCommand(statusCmd, reloadCmd, startCmd, stopCmd)
	rootCmd.Execute()
}

func isDaemonRunning() bool {
	_, err := net.Dial("unix", socketPath)
	return err == nil
}

func startDaemon() {
	execPath, err := os.Executable()
	if err != nil {
		log.Fatalf("Failed to get executable path: %v", err)
	}

	daemonCmd := exec.Command(execPath, "daemon")
	daemonCmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}

	// Detach from terminal
	daemonCmd.Stdin = nil
	daemonCmd.Stdout = nil
	daemonCmd.Stderr = nil

	if err := daemonCmd.Start(); err != nil {
		log.Fatalf("Failed to start sync service: %v", err)
	}

	// Wait for socket to become available
	for i := 0; i < 10; i++ {
		if isDaemonRunning() {
			return
		}
		// Wait a bit for the daemon to start
		time.Sleep(500 * time.Millisecond) // 500ms
	}

	log.Fatalf("Daemon failed to start within timeout")
}

func sendCommand(command, args string) Response {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return Response{Status: "error", Message: fmt.Sprintf("Failed to connect to sync service: %v", err)}
	}
	defer conn.Close()

	msg := Message{Command: command, Args: args}
	encoder := json.NewEncoder(conn)
	if err := encoder.Encode(msg); err != nil {
		return Response{Status: "error", Message: fmt.Sprintf("Failed to send command: %v", err)}
	}

	var resp Response
	decoder := json.NewDecoder(conn)
	if err := decoder.Decode(&resp); err != nil {
		return Response{Status: "error", Message: fmt.Sprintf("Failed to decode response: %v", err)}
	}

	return resp
}

// Checks if this instance should run as a daemon
func init() {
	if len(os.Args) > 1 && os.Args[1] == "daemon" {
		runDaemon()
		os.Exit(0)
	}
}

// The daemon function that will run in the background
func runDaemon() {
	// Remove existing socket if it exists
	os.Remove(socketPath)

	// Create Unix domain socket
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		log.Fatalf("Failed to create socket: %v", err)
	}

	// Start the daemon
	engine, err := NewSyncEngine()
	if err != nil {
		log.Fatalf("Failed to create sync engine: %v", err)
	}

	go engine.Start()

	// Handle client connections
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Error accepting connection: %v", err)
			continue
		}

		go handleConnection(conn, engine)
	}
}

func handleConnection(conn net.Conn, engine *SyncEngine) {
	defer conn.Close()

	var msg Message
	decoder := json.NewDecoder(conn)
	if err := decoder.Decode(&msg); err != nil {
		log.Printf("Error decoding message: %v", err)
		return
	}

	var resp Response

	switch msg.Command {
	case "status":
		status := engine.GetStatus()
		resp = Response{
			Status:  "success",
			Message: "Current sync status:",
			Data:    status,
		}
	case "reload":
		err := engine.UpdateConfig()
		if err != nil {
			resp = Response{Status: "error", Message: err.Error()}
		} else {
			resp = Response{Status: "success", Message: "Configuration reloaded successfully"}
		}

	case "sync":
		if engine.IsSyncing() {
			resp = Response{Status: "error", Message: "Wait for current sync to finish"}
		} else {
			engine.SyncAll()
			resp = Response{Status: "success", Message: "Sync started"}
		}

	case "shutdown":
		resp = Response{Status: "success", Message: "Sync service shutting down"}
		encoder := json.NewEncoder(conn)
		encoder.Encode(resp)
		os.Exit(0)
	default:
		resp = Response{Status: "error", Message: "Unknown command"}
	}

	encoder := json.NewEncoder(conn)
	encoder.Encode(resp)
}
