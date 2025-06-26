// Package daemon provides utilities for managing the application
// as a background daemon process on Unix-like systems.
// It handles starting, stopping, restarting, killing, and checking the status
// of the daemon using PID files, file locking for synchronization,
// and readiness notification via pipes.
package daemon

// Implementation notes:
// - The funcs used by the cli should log non return errors to our log package.
//   They should use fmt to print at least once before returning.
// - Error handling is lazy, functional, but could be more descriptive.
//   I'm trying to keep the code simple and readable. I'm already not a fan
//   of how big this bitch is.
// - The code is not thread safe, but the locking primitives are.
//   When testing in parallel, make a manager per test.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Data-Corruption/stdx/xlog"
	"github.com/Data-Corruption/stdx/xterm/prompt"
	"github.com/urfave/cli/v3"
)

const (
	readyFD      = 3
	pidFilePerms = 0o644
	lockFileExt  = ".lock"
)

var (
	ErrAlreadyRunning = errors.New("daemon already running")
	ErrNotRunning     = errors.New("daemon not running")
	ErrStalePID       = errors.New("stale PID file found")

	Manager *DaemonManager
)

var Command = &cli.Command{
	Name:  "daemon",
	Usage: "manually manage the daemon process",
	Commands: []*cli.Command{
		{
			Name:  "start",
			Usage: "start the daemon as a background process",
			Action: func(ctx context.Context, cmd *cli.Command) error {
				if err := Manager.Start(ctx); err != nil {
					return err
				}
				fmt.Println("Daemon started successfully.")
				return nil
			},
		},
		{
			Name:  "status",
			Usage: "check the status of the daemon",
			Action: func(ctx context.Context, cmd *cli.Command) error {
				status, err := Manager.Status(ctx)
				if err != nil {
					return err
				}
				fmt.Println("Daemon status:", status)
				return nil
			},
		},
		{
			Name:  "run",
			Usage: "run the daemon",
			Action: func(ctx context.Context, cmd *cli.Command) error {
				// TODO: Implement later
				fmt.Println("wip")
				return nil
			},
		},
		{
			Name:  "restart",
			Usage: "restart the daemon",
			Action: func(ctx context.Context, cmd *cli.Command) error {
				if err := Manager.Restart(ctx); err != nil {
					return err
				}
				fmt.Println("Daemon restarted successfully.")
				return nil
			},
		},
		{
			Name:  "stop",
			Usage: "stop the daemon",
			Action: func(ctx context.Context, cmd *cli.Command) error {
				if err := Manager.Stop(ctx); err != nil {
					return err
				}
				fmt.Println("Daemon stopped successfully.")
				return nil
			},
		},
		{
			Name:  "kill",
			Usage: "kill the daemon",
			Action: func(ctx context.Context, cmd *cli.Command) error {
				if err := Manager.Kill(ctx); err != nil {
					return err
				}
				fmt.Println("Daemon killed successfully.")
				return nil
			},
		},
	},
}

// Config holds the configuration for daemon management. All fields are required.
type Config struct {
	PIDFilePath    string        // Path to the PID file.
	ReadyTimeout   time.Duration // Max time to wait for readiness signal.
	StopTimeout    time.Duration // Max time to wait for graceful shutdown.
	DaemonRunArgs  []string      // Args to run the daemon (e.g., []string{"daemon", "run"}).
	HealthCheckURL string        // Optional URL for health checks in Status(). Non 200 responses are considered unhealthy.
}

// DaemonManager manages the daemon process.
type DaemonManager struct {
	config       Config
	lockFilePath string
}

// New creates a new Daemon manager instance.
func New(cfg Config) (*DaemonManager, error) {
	if cfg.PIDFilePath == "" {
		return nil, errors.New("PIDFilePath must be provided in Config")
	}
	if !filepath.IsAbs(cfg.PIDFilePath) {
		return nil, errors.New("PIDFilePath must be absolute")
	}
	if cfg.ReadyTimeout == 0 {
		return nil, errors.New("ReadyTimeout must be provided in Config")
	}
	if cfg.StopTimeout == 0 {
		return nil, errors.New("StopTimeout must be provided in Config")
	}
	if len(cfg.DaemonRunArgs) == 0 {
		return nil, errors.New("DaemonRunArgs must be provided in Config")
	}
	if cfg.HealthCheckURL == "" {
		return nil, errors.New("HealthCheckURL must be provided in Config")
	}
	return &DaemonManager{
		config:       cfg,
		lockFilePath: cfg.PIDFilePath + lockFileExt,
	}, nil
}

// --- File Locking Primitives ---

func (m *DaemonManager) lock(ctx context.Context) (*os.File, error) {
	lockFile, err := os.OpenFile(m.lockFilePath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("failed to open lock file %s: %w", m.lockFilePath, err)
	}
	// blocking / exclusive lock
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		if closeErr := lockFile.Close(); closeErr != nil {
			xlog.Errorf(ctx, "Failed to close lock file %s: %v", m.lockFilePath, closeErr)
		}
		return nil, fmt.Errorf("failed to acquire lock on %s: %w", m.lockFilePath, err)
	}
	return lockFile, nil
}

func (m *DaemonManager) unlock(ctx context.Context, lockFile *os.File) {
	if lockFile == nil {
		return
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN); err != nil {
		xlog.Errorf(ctx, "Failed to unlock %s: %v", m.lockFilePath, err)
	}
	if err := lockFile.Close(); err != nil {
		xlog.Errorf(ctx, "Failed to close lock file %s: %v", m.lockFilePath, err)
	}
}

// --- PID File Management ---

// readPID reads the PID from the PID file. Assumes lock is held.
func (m *DaemonManager) readPID() (int, error) {
	data, err := os.ReadFile(m.config.PIDFilePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, ErrNotRunning // Specific error type
		}
		return 0, fmt.Errorf("failed to read PID file %s: %w", m.config.PIDFilePath, err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("invalid PID value in %s: %w", m.config.PIDFilePath, err)
	}
	if pid <= 0 {
		return 0, fmt.Errorf("invalid PID value %d in %s", pid, m.config.PIDFilePath)
	}
	return pid, nil
}

// writePID writes the PID to the PID file. Assumes lock is held.
func (m *DaemonManager) writePID(pid int) error {
	return os.WriteFile(m.config.PIDFilePath, []byte(strconv.Itoa(pid)), pidFilePerms)
}

// removePID removes the PID file. Assumes lock is held.
func (m *DaemonManager) removePID() error {
	err := os.Remove(m.config.PIDFilePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove PID file %s: %w", m.config.PIDFilePath, err)
	}
	return nil
}

// --- Daemon Commands ---

// Start launches the application as a daemon.
func (m *DaemonManager) Start(ctx context.Context) error {
	lockFile, err := m.lock(ctx)
	if err != nil {
		return err
	}
	defer m.unlock(ctx, lockFile)

	// Check if already running
	pid, err := m.readPID()
	if err == nil { // PID file exists
		if IsPidAlive(pid) && IsOurBinary(pid) {
			return fmt.Errorf("%w (PID: %d)", ErrAlreadyRunning, pid)
		}
		// Stale PID file
		fmt.Fprintf(os.Stderr, "Warning: Found stale PID file %s for PID %d, removing.\n", m.config.PIDFilePath, pid)
		if err := m.removePID(); err != nil {
			// Non-fatal, proceed with starting
			fmt.Fprintf(os.Stderr, "Warning: Failed to remove stale PID file: %v\n", err)
		}
	} else if !errors.Is(err, ErrNotRunning) {
		// Error reading PID file (permissions, etc.)
		return err
	}
	// Not running or stale PID file removed, proceed to start

	selfPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	// Prepare readiness pipe
	r, w, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("failed to create readiness pipe: %w", err)
	}
	defer func() { // Close read end in parent eventually
		if err := r.Close(); err != nil {
			xlog.Errorf(ctx, "Failed to close readiness pipe read end: %v", err)
		}
	}()

	cmd := exec.Command(selfPath, m.config.DaemonRunArgs...)
	cmd.ExtraFiles = []*os.File{w} // Pass write end to child as FD 3
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // Detach completely

	if err := cmd.Start(); err != nil {
		if err := w.Close(); err != nil {
			xlog.Errorf(ctx, "Failed to close readiness pipe write end: %v", err)
		}
		return fmt.Errorf("failed to start daemon process: %w", err)
	}

	// VERY IMPORTANT: Close the write end of the pipe in the *parent*.
	// The child still has its copy. If parent holds it open, Read will block indefinitely.
	if err := w.Close(); err != nil {
		xlog.Errorf(ctx, "Failed to close readiness pipe write end: %v", err)
	}

	fmt.Printf("Daemon process started with PID: %d\n", cmd.Process.Pid)

	// Wait for readiness signal or timeout
	ready := make(chan error, 1)
	go func() {
		buf := make([]byte, 1)
		n, err := r.Read(buf) // Blocks until child writes or closes pipe
		if err != nil {
			ready <- fmt.Errorf("failed reading readiness pipe: %w", err)
		} else if n == 1 && buf[0] == '1' {
			ready <- nil // Success
		} else {
			ready <- errors.New("invalid readiness signal received")
		}
	}()

	// helper function for cleaning up the process
	cleanup := func(d time.Duration) {
		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
			xlog.Errorf(ctx, "Failed to send SIGTERM to daemon process: %v", err)
		}
		time.Sleep(d)
		if err := cmd.Process.Kill(); err != nil {
			xlog.Errorf(ctx, "Failed to kill daemon process: %v", err)
		}
		if _, err := cmd.Process.Wait(); err != nil {
			xlog.Errorf(ctx, "Failed to wait for daemon process: %v", err)
		}
	}

	select {
	case err := <-ready:
		if err != nil {
			// Process started but failed to signal readiness, Kill the disappointing child
			fmt.Fprintf(os.Stderr, "Daemon failed to signal readiness: %v\n", err)
			cleanup(m.config.StopTimeout)
			return fmt.Errorf("daemon process %d failed to become ready: %w", cmd.Process.Pid, err)
		}
		// Readiness signal received! Write PID file.
		if err := m.writePID(cmd.Process.Pid); err != nil {
			// Daemon is running, but we failed to write PID file. Critical issue. Kill the orphaned child
			fmt.Fprintf(os.Stderr, "Daemon started (PID: %d) but failed to write PID file %s: %v. Killing daemon...\n", cmd.Process.Pid, m.config.PIDFilePath, err)
			cleanup(m.config.StopTimeout)
			return fmt.Errorf("daemon started (PID: %d) but failed to write PID file %s: %w. Daemon killed", cmd.Process.Pid, m.config.PIDFilePath, err)
		}
		fmt.Println("Daemon ready.")
		return nil // Success!
	case <-time.After(m.config.ReadyTimeout):
		// Timeout waiting for readiness
		fmt.Fprintf(os.Stderr, "Timeout waiting for daemon readiness (PID: %d)\n", cmd.Process.Pid)
		cleanup(100 * time.Millisecond)
		return fmt.Errorf("timeout waiting for daemon readiness (PID: %d)", cmd.Process.Pid)
	}
}

// NotifyReady should be called by the daemon process itself once it's ready.
// Only call this after the process has passed all setup that could fail / has reached a steady ready state.
func NotifyReady(ctx context.Context) error {
	f := os.NewFile(uintptr(readyFD), "ready-pipe")
	if f != nil { // assume no pipe means manual run
		defer func() {
			if err := f.Close(); err != nil {
				xlog.Errorf(ctx, "Failed to close readiness pipe: %v", err)
			}
		}()
		_, err := f.Write([]byte{'1'})
		if err != nil {
			return fmt.Errorf("failed to write readiness signal: %w", err)
		}
	}
	return nil
}

// Status checks the status of the daemon.
func (m *DaemonManager) Status(ctx context.Context) (string, error) {
	// Use a shared lock for status check - allows multiple status checks concurrently
	lockFile, err := os.OpenFile(m.lockFilePath, os.O_RDONLY, 0o600) // Open read-only for shared lock
	if err != nil {
		if os.IsNotExist(err) {
			// If lock file doesn't exist, PID file shouldn't either
			_, pidErr := os.Stat(m.config.PIDFilePath)
			if errors.Is(pidErr, fs.ErrNotExist) {
				return "Not Running", nil
			}
			// Fall through to attempt reading PID file below, it might handle other errors
		} else {
			return "Status Unknown", fmt.Errorf("failed to open lock file %s: %w", m.lockFilePath, err)
		}
	} else {
		// Acquire shared lock
		if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_SH); err != nil {
			if err := lockFile.Close(); err != nil {
				xlog.Errorf(ctx, "Failed to close lock file %s: %v", m.lockFilePath, err)
			}
			return "Status Unknown", fmt.Errorf("failed to acquire shared lock on %s: %w", m.lockFilePath, err)
		}
		defer func() {
			if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN); err != nil {
				xlog.Errorf(ctx, "Failed to unlock %s: %v", m.lockFilePath, err)
			}
			if err := lockFile.Close(); err != nil {
				xlog.Errorf(ctx, "Failed to close lock file %s: %v", m.lockFilePath, err)
			}
		}()
	}

	pid, err := m.readPID() // Read PID file (inside lock if acquired)
	if err != nil {
		if errors.Is(err, ErrNotRunning) {
			return "Not Running", nil
		}
		// Other read errors (permissions, invalid content)
		return "Status Unknown", fmt.Errorf("error reading PID file: %w", err)
	}

	if !IsPidAlive(pid) {
		// Maybe prompt to remove stale PID file here. For now just report.
		return fmt.Sprintf("Not Running (Stale PID File: %s, PID: %d)", m.config.PIDFilePath, pid), ErrStalePID
	}

	if !IsOurBinary(pid) {
		return fmt.Sprintf("Running (PID: %d, but does NOT match expected binary!)", pid), errors.New("process PID found but is wrong binary")
	}

	// Process is alive and is our binary, check health.
	baseStatus := fmt.Sprintf("Running (PID: %d)", pid)
	if m.config.HealthCheckURL != "" {
		if err := m.healthCheck(ctx); err != nil {
			return fmt.Sprintf("%s - Unhealthy: %v", baseStatus, err), err
		}
		return fmt.Sprintf("%s - Healthy", baseStatus), nil
	}

	return baseStatus, nil // Running, no health check configured.
}

// Stop sends SIGTERM to the daemon and waits for it to exit.
func (m *DaemonManager) Stop(ctx context.Context) error {
	lockFile, err := m.lock(ctx)
	if err != nil {
		return err
	}
	defer m.unlock(ctx, lockFile)

	pid, err := m.readPID()
	if err != nil {
		if errors.Is(err, ErrNotRunning) {
			fmt.Println("Daemon not running.")
			return nil // Idempotent stop
		}
		return err // Other read errors
	}

	process, err := os.FindProcess(pid)
	if err != nil || !IsPidAlive(pid) { // Also check IsPidAlive redundantly
		fmt.Printf("Process with PID %d not found or already stopped. Removing stale PID file %s.\n", pid, m.config.PIDFilePath)
		// Clean up stale PID file
		if err := m.removePID(); err != nil {
			xlog.Errorf(ctx, "Failed to remove stale PID file %s: %v", m.config.PIDFilePath, err)
		}
		return nil
	}

	if !IsOurBinary(pid) {
		return fmt.Errorf("process with PID %d is running but is not the expected binary. Not stopping", pid)
	}

	fmt.Printf("Sending SIGTERM to daemon (PID: %d)...\n", pid)
	if err := process.Signal(syscall.SIGTERM); err != nil {
		// May happen if process died just between IsPidAlive and Signal
		if errors.Is(err, os.ErrProcessDone) {
			fmt.Println("Process already stopped.")
			if err := m.removePID(); err != nil {
				xlog.Errorf(ctx, "Failed to remove stale PID file %s: %v", m.config.PIDFilePath, err)
			}
			return nil
		}
		return fmt.Errorf("failed to send SIGTERM to PID %d: %w", pid, err)
	}

	// Wait for process to exit
	stopped := make(chan struct{})
	go func() {
		// Wait for short intervals checking if process is alive
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			if !IsPidAlive(pid) {
				close(stopped)
				return
			}
		}
	}()

	select {
	case <-stopped:
		fmt.Println("Daemon stopped gracefully.")
		return m.removePID() // Remove PID file on successful stop
	case <-time.After(m.config.StopTimeout):
		return fmt.Errorf("timeout waiting for daemon (PID: %d) to stop gracefully. Consider using 'kill'", pid)
	}
}

// Kill sends SIGKILL to the daemon process.
func (m *DaemonManager) Kill(ctx context.Context) error {
	lockFile, err := m.lock(ctx)
	if err != nil {
		return err
	}
	defer m.unlock(ctx, lockFile)

	pid, err := m.readPID()
	if err != nil {
		if errors.Is(err, ErrNotRunning) {
			fmt.Println("Daemon not running.")
			return nil // Idempotent kill
		}
		return err
	}

	process, err := os.FindProcess(pid)
	if err != nil || !IsPidAlive(pid) {
		fmt.Printf("Process with PID %d not found or already stopped. Removing stale PID file %s.\n", pid, m.config.PIDFilePath)
		if err := m.removePID(); err != nil {
			xlog.Errorf(ctx, "Failed to remove stale PID file %s: %v", m.config.PIDFilePath, err)
		}
		return nil
	}

	if !IsOurBinary(pid) {
		return fmt.Errorf("process with PID %d is running but is not the expected binary. Not killing", pid)
	}

	// Kill the process
	fmt.Printf("Sending SIGKILL to process (PID: %d)...\n", pid)
	if err := process.Signal(syscall.SIGKILL); err != nil {
		if errors.Is(err, os.ErrProcessDone) {
			fmt.Println("Process already stopped.")
			if err := m.removePID(); err != nil {
				xlog.Errorf(ctx, "Failed to remove stale PID file %s: %v", m.config.PIDFilePath, err)
			}
			return nil
		}
		// Even if SIGKILL fails, attempt to remove PID file if process is gone shortly after
		time.Sleep(100 * time.Millisecond)
		if !IsPidAlive(pid) {
			fmt.Println("Process stopped after SIGKILL attempt.")
			if err := m.removePID(); err != nil {
				xlog.Errorf(ctx, "Failed to remove stale PID file %s: %v", m.config.PIDFilePath, err)
			}
			return nil
		}
		return fmt.Errorf("failed to send SIGKILL to PID %d: %w", pid, err)
	}

	// Short wait to see if it died
	time.Sleep(200 * time.Millisecond)
	if !IsPidAlive(pid) {
		fmt.Println("Daemon killed.")
		return m.removePID()
	}

	// Should be very rare for SIGKILL to not work immediately unless zombie etc.
	return fmt.Errorf("process (PID: %d) still alive after SIGKILL", pid)
}

// Restart stops and then starts the daemon.
func (m *DaemonManager) Restart(ctx context.Context) error {
	fmt.Println("Attempting to stop daemon...")
	stopErr := m.Stop(ctx)
	if stopErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: Stop command failed: %v\n", stopErr)
		// Check if it's the timeout error - prompt user to force kill
		if strings.Contains(stopErr.Error(), "timeout waiting for daemon") {
			if prompt.YesNo("Daemon did not stop gracefully. Force kill (SIGKILL) and continue restart?") {
				killErr := m.Kill(ctx)
				if killErr != nil {
					return fmt.Errorf("failed to kill daemon during restart: %w", killErr)
				}
				fmt.Println("Daemon killed.")
			} else {
				return errors.New("restart aborted because daemon did not stop gracefully")
			}
		} else if !errors.Is(stopErr, ErrNotRunning) && !strings.Contains(stopErr.Error(), "already stopped") {
			return fmt.Errorf("aborting restart due to stop error: %w", stopErr)
		}
		// If it was ErrNotRunning or similar "already stopped" message, continue.
	} else {
		fmt.Println("Daemon stopped.")
	}

	fmt.Println("Starting daemon...")
	startErr := m.Start(ctx)
	if startErr != nil {
		return fmt.Errorf("failed to start daemon during restart: %w", startErr)
	}

	fmt.Println("Restart completed.")
	return nil
}

// --- Helper Functions ---

// IsPidAlive checks if a process with the given PID exists.
func IsPidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false // Error finding process (e.g., permission denied on some systems?)
	}
	// Sending signal 0 doesn't actually send a signal, but checks if the process exists.
	err = process.Signal(syscall.Signal(0))
	// On Unix systems, err == nil means process exists.
	// os.ErrProcessDone means it existed recently but is now gone.
	// Other errors (like permission errors) might occur, conservatively return false.
	return err == nil
}

// IsOurBinary checks if the process with the given PID is running the same executable
// as the current process. This is Linux-specific (/proc).
func IsOurBinary(pid int) bool {
	if pid <= 0 {
		return false
	}
	exePath := fmt.Sprintf("/proc/%d/exe", pid)
	target, err := os.Readlink(exePath)
	if err != nil {
		return false // Cannot read link (process gone, permissions, not Linux)
	}

	self, err := os.Executable()
	if err != nil {
		return false // Cannot get own executable path
	}

	// Resolve symlinks for both paths for robust comparison
	selfReal, errSelf := filepath.EvalSymlinks(self)
	targetReal, errTarget := filepath.EvalSymlinks(target)

	// If symlink resolution fails, fall back to original paths maybe?
	// Or consider it a mismatch? Let's be strict: successful resolution needed.
	if errSelf != nil || errTarget != nil {
		// Fallback to comparing non-resolved paths if resolution failed
		// This handles cases where /proc/pid/exe is a link to deleted file but proc entry still exists
		// or other edge cases.
		return self == target
	}

	return selfReal == targetReal
}

// healthCheck (Placeholder - Implement actual HTTP GET)
func (m *DaemonManager) healthCheck(ctx context.Context) error {
	if m.config.HealthCheckURL == "" {
		return errors.New("health check URL not configured")
	}
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(m.config.HealthCheckURL)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			xlog.Errorf(ctx, "Failed to close response body: %v", err)
		}
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("received non-2xx status code: %d", resp.StatusCode)
	}
	return nil
}
