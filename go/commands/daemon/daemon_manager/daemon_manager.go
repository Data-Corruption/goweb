// Package daemon provides utilities for managing the application
// as a background daemon process on Unix-like systems.
// It handles starting, stopping, restarting, and checking the status
// of the daemon using a PID file, flock for synchronization,
// and readiness notification via pipes.
//
// How this works:
//  1. Creates a DaemonManager, use IntoContext(), place it in the urfave/cli app context.
//  2. Add the command from ../command.go to the urfave/cli app.
//  3. Modify the server in ../command.go as needed.
//
// Impl notes:
//   - Command funcs handle console output directly. Doing everything via return values would be too rigid.
//   - When testing, make a manager per test, override binPath in start_internal() to point to a test binary.
//   - flock instead of just PID in lmdb so it's more portable.
//   - not meant for long-running management, just for single action then exit.
package daemon_manager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Data-Corruption/stdx/xlog"
)

type contextKey string

const (
	READY_FD       = 3 // file descriptor for the readiness pipe in the child process.
	PID_FILE_PERMS = 0o600

	CTX_KEY contextKey = "daemon_manager"
)

// DaemonManager manages the daemon process. PID file is opened on first use.
// Limits: One manager per context. One manager per process, not thread safe within the same process.
type DaemonManager struct {
	PIDFilePath   string        // Path to the PID file. E.g. "/var/run/daemon.pid".
	ReadyTimeout  time.Duration // Max time to wait for readiness signal.
	StopTimeout   time.Duration // Max time to wait for graceful shutdown.
	DaemonRunArgs []string      // Args to run the daemon (e.g., []string{"daemon", "run"}).
	pidFile       *os.File      // empty/0 means not running
}

// Init creates a new DaemonManager and inserts it into the given context.
func IntoContext(ctx context.Context, manager *DaemonManager) (context.Context, error) {
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

// Close closes the PID file if it's open.
func (m *DaemonManager) Close() error {
	if m.pidFile != nil {
		if err := m.pidFile.Close(); err != nil {
			return fmt.Errorf("failed to close PID file %s: %w", m.PIDFilePath, err)
		}
		m.pidFile = nil // reset to allow reopening
	}
	return nil
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
	if m.pidFile == nil {
		m.pidFile, err = os.OpenFile(m.PIDFilePath, os.O_CREATE|os.O_RDWR, PID_FILE_PERMS)
		if err != nil {
			return fmt.Errorf("failed to open PID file %s: %w", m.PIDFilePath, err)
		}
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
}

// readPID reads the PID from the PID file. Assumes lock is held.
func (m *DaemonManager) readPID() (int, error) {
	if m.pidFile == nil {
		return 0, fmt.Errorf("PID file %s is not open", m.PIDFilePath)
	}
	if _, err := m.pidFile.Seek(0, io.SeekStart); err != nil {
		return 0, fmt.Errorf("failed to seek PID file %s: %w", m.PIDFilePath, err)
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
	if err := m.pidFile.Truncate(0); err != nil {
		return fmt.Errorf("failed to truncate PID file %s: %w", m.PIDFilePath, err)
	}
	if _, err := m.pidFile.Seek(0, 0); err != nil {
		return fmt.Errorf("failed to seek PID file %s: %w", m.PIDFilePath, err)
	}
	if _, err := m.pidFile.WriteString(strconv.Itoa(pid)); err != nil {
		return fmt.Errorf("failed to write PID to %s: %w", m.PIDFilePath, err)
	}
	if err := m.pidFile.Sync(); err != nil {
		return fmt.Errorf("failed to sync PID file %s: %w", m.PIDFilePath, err)
	}
	return nil
}

// --- Readiness Stuff ---

// NotifyReady should be called by the daemon process itself once it's ready.
// Only call this after the process has passed all setup that could fail / has reached a steady ready state.
func NotifyReady(ctx context.Context) error {
	if os.Getenv("START_NOTIFY") != "1" {
		return nil
	}

	f := os.NewFile(uintptr(READY_FD), "ready-pipe")
	if f == nil {
		return fmt.Errorf("failed to get readiness pipe from FD %d: %w", READY_FD, os.ErrInvalid)
	}

	defer func() {
		if err := f.Close(); err != nil {
			xlog.Errorf(ctx, "Failed to close readiness pipe: %v", err)
		}
	}()
	_, err := f.Write([]byte{'1'})
	if err != nil {
		return fmt.Errorf("failed to write readiness signal: %w", err)
	}

	return nil
}

// --- Daemon Commands ---

// Start launches the application as a daemon.
func (m *DaemonManager) Start(ctx context.Context) error {
	selfPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}
	return m.start(ctx, selfPath)
}

// start launches the application as a daemon.
// It's split up like this to allow concurrent testing.
func (m *DaemonManager) start(ctx context.Context, binPath string) error {
	if err := m.lockPID(ctx); err != nil {
		return err
	}
	defer m.unlockPID(ctx)

	// check if already running
	pid, err := m.readPID()
	if err != nil {
		return fmt.Errorf("failed to read PID file %s: %w", m.PIDFilePath, err)
	} else if pid != 0 {
		// check if the process is running / is our binary
		if running, err := status(pid); err != nil {
			return fmt.Errorf("failed to check if process %d is running our binary: %w", pid, err)
		} else if !running {
			xlog.Errorf(ctx, "Process with PID %d is not running or a different binary. Cleaning stale PID file...", pid)
			if err := m.writePID(0); err != nil {
				return fmt.Errorf("failed to clean stale PID file %s: %w", m.PIDFilePath, err)
			}
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

	cmd := exec.Command(binPath, m.DaemonRunArgs...)
	cmd.ExtraFiles = []*os.File{w} // pass write end to child as FD 3
	cmd.Env = append(os.Environ(), "START_NOTIFY=1")
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

	select {
	case err := <-ready:
		// if process started but failed to signal readiness, kill the disappointing child
		if err != nil {
			fmt.Fprint(os.Stderr, "Daemon process started but failed to signal readiness, cleaning up...\n")
			xlog.Errorf(ctx, "Daemon process %d failed to signal readiness: %v", cmd.Process.Pid, err)
			return m.stop(ctx, cmd.Process)
		}
		// readiness signal received! Write PID file.
		if err := m.writePID(cmd.Process.Pid); err != nil {
			// failed to write PID file. Kill the orphaned child. This is so sad, Alexa play Chamber Of Reflection by Mac DeMarco.
			fmt.Fprint(os.Stderr, "Daemon started but failed to write PID file, cleaning up...\n")
			xlog.Errorf(ctx, "Daemon process %d started but failed to write PID file %s: %v", cmd.Process.Pid, m.PIDFilePath, err)
			return m.stop(ctx, cmd.Process)
		}
		fmt.Println("Daemon ready")
		return nil // success
	case <-time.After(m.ReadyTimeout):
		fmt.Fprint(os.Stderr, "Daemon process did not signal readiness within the timeout, cleaning up...\n")
		xlog.Errorf(ctx, "Daemon process %d did not signal readiness within %s", cmd.Process.Pid, m.ReadyTimeout)
		return m.stop(ctx, cmd.Process)
	}
}

// Status checks the status of the daemon.
func (m *DaemonManager) Status(ctx context.Context) error {
	if err := m.lockPID(ctx); err != nil {
		return err
	}
	defer m.unlockPID(ctx)

	// read PID from file
	pid, err := m.readPID()
	if err != nil {
		return fmt.Errorf("failed to read PID file %s: %w", m.PIDFilePath, err)
	} else if pid == 0 {
		fmt.Println("Daemon not running.")
		xlog.Debug(ctx, "Daemon not running, PID file is empty")
		return nil
	}

	// check if the process is running / is our binary
	if running, err := status(pid); err != nil {
		return fmt.Errorf("failed to check if process %d is running our binary: %w", pid, err)
	} else if !running {
		xlog.Errorf(ctx, "Process with PID %d is not running or a different binary. Cleaning stale PID file...", pid)
		fmt.Println("Daemon not running.")
		return m.writePID(0)
	}

	fmt.Println("Daemon is running.")
	return nil
}

// Stop shuts down the daemon process.
// It sends SIGTERM and waits for graceful shutdown, then escalates to SIGKILL if it exceeds the timeout.
func (m *DaemonManager) Stop(ctx context.Context) error {
	if err := m.lockPID(ctx); err != nil {
		return err
	}
	defer m.unlockPID(ctx)

	// read PID from file
	pid, err := m.readPID()
	if err != nil {
		return fmt.Errorf("failed to read PID file %s: %w", m.PIDFilePath, err)
	} else if pid == 0 {
		fmt.Println("Daemon not running.")
		xlog.Debug(ctx, "Daemon not running, PID file is empty")
		return nil
	}

	// check if the process is running / is our binary
	if running, err := status(pid); err != nil {
		return fmt.Errorf("failed to check if process %d is running our binary: %w", pid, err)
	} else if !running {
		xlog.Errorf(ctx, "Process with PID %d is not running or a different binary. Cleaning stale PID file...", pid)
		fmt.Println("Daemon not running.")
		return m.writePID(0)
	}

	// get process handle by PID
	process, err := os.FindProcess(pid)
	if err != nil {
		if errors.Is(err, os.ErrProcessDone) {
			fmt.Println("Daemon not running.")
			xlog.Debug(ctx, "Daemon not running, process already exited")
			return m.writePID(0)
		}
		fmt.Fprint(os.Stderr, "Error finding daemon process, see logs for details.\n")
		m.writePID(0)
		return fmt.Errorf("failed to find process with PID %d: %w", pid, err)
	}

	err = m.stop(ctx, process)
	if err == nil {
		fmt.Println("Daemon stopped.")
		xlog.Debugf(ctx, "Daemon process %d stopped successfully", pid)
	}
	return err
}

// Restart stops and then starts the daemon.
func (m *DaemonManager) Restart(ctx context.Context) error {
	// stop
	fmt.Println("Stopping daemon...")
	if err := m.Stop(ctx); err != nil {
		return fmt.Errorf("failed to stop daemon: %w", err)
	}
	// sleep 1s so os/fs can catch it's breath
	time.Sleep(1 * time.Second)
	// start
	fmt.Println("Starting daemon...")
	return m.Start(ctx)
}

// --- Helper Functions ---

// stop shuts down the daemon process. Starts with a graceful SIGTERM and
// escalates to SIGKILL if exceeds the timeout.
func (m *DaemonManager) stop(ctx context.Context, p *os.Process) error {
	if err := m.writePID(0); err != nil {
		xlog.Errorf(ctx, "Failed to write 0 to PID file %s: %v", m.PIDFilePath, err)
	}

	if err := p.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("failed to send SIGTERM to daemon process: %w", err)
	}

	xlog.Debugf(ctx, "Sent SIGTERM to daemon process %d", p.Pid)

	done := make(chan error, 1)
	go func() {
		// check if the process is still running via status every 500ms
		// Can't use p.Wait() because that only works with children of the current process.
		for {
			if running, err := status(p.Pid); err != nil {
				done <- fmt.Errorf("failed to check if process %d is running: %w", p.Pid, err)
				return
			} else if !running {
				done <- nil
				return
			}
			time.Sleep(500 * time.Millisecond)
		}
	}()

	select {
	case err := <-done:
		return err
	case <-time.After(m.StopTimeout):
		if err := p.Kill(); err != nil {
			return fmt.Errorf("failed to kill daemon process: %w", err)
		}
		return fmt.Errorf("daemon process shut down forcefully after timeout")
	}
}

// status checks if the process with the given PID is running the same
// executable path as the current process. This is Linux-specific (/proc).
// Returns true if the process is running our binary, false if not running or different binary.
func status(pid int) (bool, error) {
	if pid <= 0 {
		return false, nil
	}

	exePath := fmt.Sprintf("/proc/%d/exe", pid)
	target, err := os.Readlink(exePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("failed to read link %s: %w", exePath, err)
	}

	self, err := os.Executable()
	if err != nil {
		return false, fmt.Errorf("failed to get executable path: %w", err)
	}

	// resolve symlinks
	selfReal, errSelf := filepath.EvalSymlinks(self)
	targetReal, errTarget := filepath.EvalSymlinks(target)

	if errSelf != nil || errTarget != nil {
		return self == target, nil // raw path fallback
	}
	return selfReal == targetReal, nil
}
