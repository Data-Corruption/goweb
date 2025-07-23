package update

import (
	"context"
	"fmt"
	"goweb/go/commands/daemon/daemon_manager"
	"goweb/go/evil"
	"goweb/go/storage/config"
	"goweb/go/system/git"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/Data-Corruption/stdx/xlog"
	"github.com/Data-Corruption/stdx/xterm/prompt"
	"golang.org/x/mod/semver"
)

const (
	RepoURL          = "https://github.com/Data-Corruption/goweb.git"
	InstallScriptURL = "https://raw.githubusercontent.com/Data-Corruption/goweb/main/scripts/install.sh"
)

// Check checks if there is a newer version of the application available and updates the config accordingly.
// It returns true if an update is available, false otherwise.
// When running a dev build (e.g. with `vX.X.X`), it returns false without checking.
func Check(ctx context.Context, version string) (bool, error) {
	if version == "vX.X.X" {
		return false, nil // No version set, no update check needed
	}

	lCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	latest, err := git.LatestGitHubReleaseTag(lCtx, RepoURL)
	if err != nil {
		return false, err
	}

	updateAvailable := semver.Compare(latest, version) > 0
	xlog.Debugf(ctx, "Latest version: %s, Current version: %s, Update available: %t", latest, version, updateAvailable)

	// update config
	if err := config.Set(ctx, "updateAvailable", updateAvailable); err != nil {
		return false, err
	}

	return updateAvailable, nil
}

// update checks if there is a newer version of the tool available.
// If a newer version is available, it will stop the daemon then spawn a new process to facilitate the update.
func update(ctx context.Context, version string) error {
	if version == "vX.X.X" {
		fmt.Println("Dev build detected, skipping update.")
		return nil
	}

	lCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	latest, err := git.LatestGitHubReleaseTag(lCtx, RepoURL)
	if err != nil {
		return err
	}

	updateAvailable := semver.Compare(latest, version) > 0
	if !updateAvailable {
		fmt.Println("No updates available.")
		return nil
	}
	fmt.Println("New version available:", latest)

	// get if sudo
	isRoot := os.Geteuid() == 0

	// get the executable path
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}
	selfReal, errSelf := filepath.EvalSymlinks(self)
	if errSelf != nil {
		selfReal = self // fallback to self if symlink resolution fails
	}
	// ensure the path is absolute
	selfPath, err := filepath.Abs(selfReal)
	if err != nil {
		return fmt.Errorf("failed to get absolute path of executable: %w", err)
	}

	runSudo := false
	if !isRoot {
		if filepath.Dir(selfPath) == "/usr/local/bin" {
			if runSudo, err = prompt.YesNo("This update requires root privileges. Do you want to run the update with sudo?"); err != nil {
				return fmt.Errorf("failed to prompt for sudo: %w", err)
			}
			if !runSudo {
				fmt.Println("Update aborted. Please run the command with sudo to update.")
				return nil
			}
		}
		// if not "$HOME/.local/bin"
		if filepath.Dir(selfPath) != filepath.Join(os.Getenv("HOME"), ".local", "bin") {
			if runSudo, err = prompt.YesNo("Unsure if sudo is required. Do you want to run the update with sudo?"); err != nil {
				return fmt.Errorf("failed to prompt for sudo: %w", err)
			}
		}
	}

	// run the install command
	pipeline := fmt.Sprintf("curl -sSfL %s | %sbash -s -- latest %q", InstallScriptURL, evil.Ternary(runSudo, "sudo ", ""), filepath.Dir(selfPath))
	xlog.Debugf(ctx, "Running update command: %s", pipeline)

	iCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(iCtx, "bash", "-c", pipeline)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("update failed: %w", err)
	}

	// update config
	if err := config.Set(ctx, "updateAvailable", false); err != nil {
		return fmt.Errorf("failed to set updateAvailable in config: %w", err)
	}

	// restart the daemon
	fmt.Println("Ensuring daemon is up to date by restart...")
	manager, err := daemon_manager.FromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get daemon manager: %w", err)
	}
	if err := manager.Restart(ctx); err != nil {
		return fmt.Errorf("failed to restart daemon: %w", err)
	}

	return nil
}
