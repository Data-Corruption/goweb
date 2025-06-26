package daemon

// Super basic tests, mainly for just utilities, still don't have any involving inter-process stuff

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Data-Corruption/stdx/xlog"
)

// TestPIDFileManagement verifies writePID, readPID, and removePID.
func TestPIDFileManagement(t *testing.T) {
	// Create a temporary directory for testing.
	tmpDir, err := os.MkdirTemp("", "daemon_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			t.Fatalf("Failed to remove temp dir: %v", err)
		}
	}()

	pidFile := filepath.Join(tmpDir, "daemon.pid")
	cfg := Config{
		PIDFilePath:    pidFile,
		ReadyTimeout:   2 * time.Second,
		StopTimeout:    1 * time.Second,
		DaemonRunArgs:  []string{"daemon", "run"},
		HealthCheckURL: "http://localhost:8080/health",
	}

	m, err := New(cfg)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	// Test writing PID.
	testPID := 12345
	if err := m.writePID(testPID); err != nil {
		t.Fatalf("writePID failed: %v", err)
	}

	// Test reading PID.
	readPID, err := m.readPID()
	if err != nil {
		t.Fatalf("readPID failed: %v", err)
	}
	if readPID != testPID {
		t.Errorf("readPID returned %d; want %d", readPID, testPID)
	}

	// Test removing PID.
	if err := m.removePID(); err != nil {
		t.Fatalf("removePID failed: %v", err)
	}
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Errorf("PID file still exists after removal")
	}
}

// TestIsPidAlive verifies that the current process is detected as alive.
func TestIsPidAlive(t *testing.T) {
	pid := os.Getpid()
	if !IsPidAlive(pid) {
		t.Errorf("IsPidAlive(%d) returned false; expected true", pid)
	}
}

// TestIsOurBinary verifies that the current process is recognized as our binary.
// Note: This test is Linux-specific as it relies on /proc.
func TestIsOurBinary(t *testing.T) {
	pid := os.Getpid()
	if !IsOurBinary(pid) {
		t.Errorf("IsOurBinary(%d) returned false; expected true", pid)
	}
}

// TestLockUnlock ensures that acquiring and releasing the lock works.
func TestLockUnlock(t *testing.T) {
	// Create a temporary directory for testing.
	tmpDir, err := os.MkdirTemp("", "daemon_test_lock")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			t.Fatalf("Failed to remove temp dir: %v", err)
		}
	}()

	pidFile := filepath.Join(tmpDir, "daemon.pid")
	cfg := Config{
		PIDFilePath:    pidFile,
		ReadyTimeout:   2 * time.Second,
		StopTimeout:    1 * time.Second,
		DaemonRunArgs:  []string{"daemon", "run"},
		HealthCheckURL: "http://localhost:8080/health",
	}

	m, err := New(cfg)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	// create context with a logger
	ctx := context.Background()
	// Create a temporary directory for the logger.
	logDir, err := os.MkdirTemp("", "daemon_test_log")
	if err != nil {
		t.Fatalf("Failed to create temp log dir: %v", err)
	}
	defer func() {
		if err := os.RemoveAll(logDir); err != nil {
			t.Fatalf("Failed to remove temp log dir: %v", err)
		}
	}()
	log, err := xlog.New(logDir, "error")
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	ctx = xlog.IntoContext(ctx, log)

	// Acquire lock.
	lockFile, err := m.lock(ctx)
	if err != nil {
		t.Fatalf("lock() failed: %v", err)
	}

	// Release lock.
	m.unlock(ctx, lockFile)

	// Acquire lock again to ensure it's reusable.
	lockFile, err = m.lock(ctx)
	if err != nil {
		t.Fatalf("lock() after unlock failed: %v", err)
	}
	m.unlock(ctx, lockFile)
}

// TestInvalidPIDFile ensures that an invalid PID value is caught.
func TestInvalidPIDFile(t *testing.T) {
	// Create a temporary PID file with invalid content.
	tmpFile, err := os.CreateTemp("", "invalid_pid")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer func() {
		if err := os.Remove(tmpFile.Name()); err != nil {
			t.Fatalf("Failed to remove temp file: %v", err)
		}
	}()

	invalidContent := "not_a_number"
	if err := os.WriteFile(tmpFile.Name(), []byte(invalidContent), pidFilePerms); err != nil {
		t.Fatalf("Failed to write invalid PID: %v", err)
	}

	cfg := Config{
		PIDFilePath:    tmpFile.Name(),
		ReadyTimeout:   2 * time.Second,
		StopTimeout:    1 * time.Second,
		DaemonRunArgs:  []string{"daemon", "run"},
		HealthCheckURL: "http://localhost:8080/health",
	}

	m, err := New(cfg)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	_, err = m.readPID()
	if err == nil {
		t.Error("Expected error reading invalid PID, got nil")
	}
}
