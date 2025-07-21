package update

import (
	"context"
	"goweb/go/system/git"
	"time"

	"golang.org/x/mod/semver"
)

// Update checks if there is a newer version of the tool available.
// If a newer version is available, it prompts the user to update.
// If the user agrees, it will stop the daemon then spawn a new process to facilitate the update.
func Update(repoURL string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	latest, err := git.LatestGitHubReleaseTag(ctx, repoURL)
	if err != nil {
		return err
	}

	updateAvailable := semver.Compare(latest, Version) > 0

	// TODO: Implement

	return nil
}
