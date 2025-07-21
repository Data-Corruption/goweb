// Package daemon provides utilities for managing the application
// as a background daemon process on Unix-like systems.
// It handles starting, stopping, restarting, killing, and checking the status
// of the daemon using PID files, file locking for synchronization,
// and readiness notification via pipes.
//
// How this works:
//  1. Init creates a DaemonManager and inserts it into a context.
//  2. Use that context in urfave/cli
//  3. Add the command from ../command.go to the CLI app.
//  4. Modify the server in ../command.go as needed.
package daemon_manager

// Impl notes:
// - Command funcs should print at least once before returning for the user.
//   This is since messaging via returning errors is too rigid in this case.
// - When testing, make a manager per test.
// - flock instead of lmdb so it's more portable.

import (
	"context"
	"errors"
	"fmt"
	"io"
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
)

const (
	READY_FD       = 3 // file descriptor for the readiness pipe in the child process.
	PID_FILE_PERMS = 0o644
	CTX_KEY        = "daemon_manager"
)

// DaemonManager manages the daemon process.
type DaemonManager struct {
	PIDFilePath   string        // Path to the PID file. E.g. "/var/run/daemon.pid".
	ReadyTimeout  time.Duration // Max time to wait for readiness signal.
	StopTimeout   time.Duration // Max time to wait for graceful shutdown.
	DaemonRunArgs []string      // Args to run the daemon (e.g., []string{"daemon", "run"}).
	pidFile       *os.File      // empty/0 means not running
}

// Init creates a new DaemonManager and inserts it into the given context.
func IntoContext(ctx context.Context, manager DaemonManager) (context.Context, error) {
	if err := manager.validate(); err != nil {
		return nil, fmt.Errorf("invalid daemon manager config: %w", err)
	}
	return context.WithValue(ctx, CTX_KEY, manager), nil
}

func FromContext(ctx context.Context) (*DaemonManager, error) {
	manager, ok := ctx.Value(CTX_KEY).(*DaemonManager)
	if !ok || manager == nil {
		return nil, fmt.Errorf("daemon manager not found in context, did you call Init()?")
	}
	if err := manager.validate(); err != nil {
		return nil, fmt.Errorf("invalid daemon manager config: %w", err)
	}
	return manager, nil
}

func (m *DaemonManager) validate() error {
	if m.PIDFilePath == "" {
		return errors.New("PIDFilePath must be provided")
	}
	if !filepath.IsAbs(m.PIDFilePath) {
		return errors.New("PIDFilePath must be absolute")
	}
	if m.ReadyTimeout == 0 {
		return errors.New("ReadyTimeout must be provided")
	}
	if m.StopTimeout == 0 {
		return errors.New("StopTimeout must be provided")
	}
	if len(m.DaemonRunArgs) == 0 {
		return errors.New("DaemonRunArgs must be provided")
	}
	return nil
}

// --- PID File ---

func (m *DaemonManager) lockPID(ctx context.Context) error {
	var err error
	m.pidFile, err = os.OpenFile(m.PIDFilePath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("failed to open PID file %s: %w", m.PIDFilePath, err)
	}
	// blocking / exclusive lock
	if err := syscall.Flock(int(m.pidFile.Fd()), syscall.LOCK_EX); err != nil {
		if closeErr := m.pidFile.Close(); closeErr != nil {
			xlog.Errorf(ctx, "Failed to close PID file %s: %v", m.PIDFilePath, closeErr)
		}
		return fmt.Errorf("failed to acquire lock on %s: %w", m.PIDFilePath, err)
	}
	return nil
}

func (m *DaemonManager) unlockPID(ctx context.Context) {
	if m.pidFile == nil {
		return
	}
	if err := syscall.Flock(int(m.pidFile.Fd()), syscall.LOCK_UN); err != nil {
		xlog.Errorf(ctx, "Failed to unlock %s: %v", m.PIDFilePath, err)
	}
	if err := m.pidFile.Close(); err != nil {
		xlog.Errorf(ctx, "Failed to close PID file %s: %v", m.PIDFilePath, err)
	}
}

// readPID reads the PID from the PID file. Assumes lock is held.
func (m *DaemonManager) readPID() (int, error) {
	if m.pidFile == nil {
		return 0, fmt.Errorf("PID file %s is not open", m.PIDFilePath)
	}
	data, err := io.ReadAll(m.pidFile)
	if err != nil {
		return 0, fmt.Errorf("failed to read PID file %s: %w", m.PIDFilePath, err)
	}
	str := strings.TrimSpace(string(data))
	if str == "" {
		return 0, nil // empty file means not running
	}
	pid, err := strconv.Atoi(str)
	if err != nil {
		return 0, fmt.Errorf("invalid PID value in %s: %w", m.PIDFilePath, err)
	}
	return pid, nil
}

// writePID writes the PID to the PID file. Assumes lock is held.
func (m *DaemonManager) writePID(pid int) error {
	if m.pidFile == nil {
		return fmt.Errorf("PID file %s is not open", m.PIDFilePath)
	}
	_, err := m.pidFile.WriteString(strconv.Itoa(pid))
	return err
}

// --- Readiness Stuff ---

// NotifyReady should be called by the daemon process itself once it's ready.
// Only call this after the process has passed all setup that could fail / has reached a steady ready state.
func NotifyReady(ctx context.Context) error {
	f := os.NewFile(uintptr(READY_FD), "ready-pipe")
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

// --- Daemon Commands ---

// Start launches the application as a daemon.
func (m *DaemonManager) Start(ctx context.Context) error {
	if err := m.lockPID(ctx); err != nil {
		return err
	}
	defer m.unlockPID(ctx)

	// check if already running
	if pid, err := m.readPID(); err != nil {
		return fmt.Errorf("failed to read PID file %s: %w", m.PIDFilePath, err)
	} else if pid > 0 {
		pidAlive, err := IsPidAlive(pid)
		if err != nil {
			return fmt.Errorf("failed to check if PID %d is alive: %w", pid, err)
		}
		if pidAlive && IsOurBinary(pid) {
			fmt.Println("Daemon is already running.")
			return nil
		}
	}

	// prepare readiness pipe
	r, w, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("failed to create readiness pipe: %w", err)
	}
	defer func() { // close read end in parent eventually
		if err := r.Close(); err != nil {
			xlog.Errorf(ctx, "Failed to close readiness pipe read end: %v", err)
		}
	}()

	selfPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	cmd := exec.Command(selfPath, m.DaemonRunArgs...)
	cmd.ExtraFiles = []*os.File{w} // pass write end to child as FD 3
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // detach completely

	err = cmd.Start()
	// VERY IMPORTANT: Close the write end of the pipe in the *parent*.
	// The child still has its copy. If parent holds it open, Read will block indefinitely.
	if err := w.Close(); err != nil {
		xlog.Errorf(ctx, "Failed to close readiness pipe write end: %v", err)
	}
	if err != nil {
		return fmt.Errorf("failed to start daemon process: %w", err)
	}

	xlog.Debugf(ctx, "Daemon process started, PID: %d\n", cmd.Process.Pid)

	// wait for readiness signal or timeout
	ready := make(chan error, 1)
	go func() {
		buf := make([]byte, 1)
		n, err := r.Read(buf) // blocks until child writes or closes pipe
		if err != nil {
			ready <- fmt.Errorf("failed reading readiness pipe: %w", err)
		} else if n == 1 && buf[0] == '1' {
			ready <- nil // successful readiness signal
		} else {
			ready <- errors.New("invalid readiness signal received")
		}
	}()

	// helper for cleaning up bad starts
	cleanup := func() error {
		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
			return fmt.Errorf("Failed to send SIGTERM to daemon process: %w", err)
		}

		waitErr := make(chan error, 1)
		go func() {
			_, err := cmd.Process.Wait()
			waitErr <- err
		}()

		select {
		case err := <-waitErr:
			if err != nil && !errors.Is(err, os.ErrProcessDone) {
				return fmt.Errorf("Daemon process exited with error: %w", err)
			}
			return nil
		case <-time.After(m.StopTimeout):
			if err := cmd.Process.Kill(); err != nil {
				return fmt.Errorf("Failed to kill daemon process: %w", err)
			}
			return fmt.Errorf("Daemon process shut down forcefully after timeout")
		}
	}

	select {
	case err := <-ready:
		// if process started but failed to signal readiness, kill the disappointing child
		if err != nil {
			fmt.Fprint(os.Stderr, "Daemon process started but failed to signal readiness, cleaning up...\n")
			xlog.Errorf(ctx, "Daemon process %d failed to signal readiness: %w", cmd.Process.Pid, err)
			return cleanup()
		}
		// readiness signal received! Write PID file.
		if err := m.writePID(cmd.Process.Pid); err != nil {
			// failed to write PID file. Kill the orphaned child. This is so sad, Alexa play Chamber Of Reflection by Mac DeMarco.
			fmt.Fprint(os.Stderr, "Daemon started but failed to write PID file, cleaning up...\n")
			xlog.Errorf(ctx, "Daemon process %d started but failed to write PID file %s: %w", cmd.Process.Pid, m.PIDFilePath, err)
			return cleanup()
		}
		fmt.Println("🟢 Daemon ready")
		return nil // success
	case <-time.After(m.ReadyTimeout):
		fmt.Fprint(os.Stderr, "Daemon process did not signal readiness within the timeout, cleaning up...\n")
		xlog.Errorf(ctx, "Daemon process %d did not signal readiness within %s", cmd.Process.Pid, m.ReadyTimeout)
		return cleanup()
	}
}

// Status checks the status of the daemon.
// Returns a string describing the status and an error if any.
func (m *DaemonManager) Status(ctx context.Context) (string, error) {
	// use a shared lock for status check - allows multiple status checks concurrently. open read-only for shared lock
	if lockFile, err := os.OpenFile(m.lockFilePath, os.O_RDONLY, 0o600); err != nil {
		if os.IsNotExist(err) {
			// case where lock file has not been created yet or user is messing with files manually.
			// Not full proof but good enough for now.
			return "Not Running", nil
		}
		return "Status Unknown", fmt.Errorf("failed to open lock file %s: %w", m.lockFilePath, err)
	} else {
		// acquire the shared lock
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
	lockFile, err := m.lockPID(ctx)
	if err != nil {
		return err
	}
	defer m.unlockPID(ctx, lockFile)

	pid, err := m.readPID()
	if err != nil {
		if errors.Is(err, ErrNotRunning) {
			fmt.Println("Daemon not running.")
			return nil
		}
		return err
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
	lockFile, err := m.lockPID(ctx)
	if err != nil {
		return err
	}
	defer m.unlockPID(ctx, lockFile)

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
			force, err := prompt.YesNo("Daemon did not stop gracefully. Force kill (SIGKILL) and continue restart?")
			if err != nil {
				return fmt.Errorf("failed to read user input for force kill: %w", err)
			}
			if force {
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
func IsPidAlive(pid int) (bool, error) {
	if pid <= 0 {
		return false, nil
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return false, err
	}

	// sending signal 0 doesn't actually send a signal, but checks if the process exists.
	err = process.Signal(syscall.Signal(0))
	if err != nil && !errors.Is(err, os.ErrProcessDone) { // os.ErrProcessDone means it existed recently but is now gone.
		return false, fmt.Errorf("failed to check if process %d is alive: %w", pid, err)
	}
	return true, nil
}

// IsOurBinary checks if the process with the given PID is running the same executable
// as the current process. This is Linux-specific (/proc).
// Returns false on any error to be safe.
func IsOurBinary(pid int) bool {
	if pid <= 0 {
		return false
	}

	exePath := fmt.Sprintf("/proc/%d/exe", pid)
	target, err := os.Readlink(exePath)
	if err != nil {
		return false
	}

	self, err := os.Executable()
	if err != nil {
		return false
	}

	// resolve symlinks
	selfReal, errSelf := filepath.EvalSymlinks(self)
	targetReal, errTarget := filepath.EvalSymlinks(target)

	if errSelf != nil || errTarget != nil {
		return self == target // raw path fallback
	}
	return selfReal == targetReal
}

// healthCheck performs a health check by making a GET request to the configured URL.
// Returns an error if the request fails or returns a non-2xx status code.
func (m *DaemonManager) healthCheck(ctx context.Context) error {
	if m.config.HealthCheckURL == "" {
		return errors.New("health check URL not configured")
	}

	xlog.Debugf(ctx, "Performing health check on %s", m.config.HealthCheckURL)

	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(m.config.HealthCheckURL)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("received non-2xx status code: %d", resp.StatusCode)
	}
	return nil
}
