package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"

	"go.uber.org/zap"
)

// CloneRepo performs a shallow clone of repoURL into destDir, streaming every
// line of git's stdout/stderr to the logger under stream="git".
// Any pre-existing directory at destDir is removed first so that the binary
// is idempotent when the Kubernetes Job retries on a node that still has the
// previous run's work directory.
func CloneRepo(ctx context.Context, repoURL, destDir string, log *zap.Logger) error {
	if err := os.RemoveAll(destDir); err != nil {
		return fmt.Errorf("clean work dir: %w", err)
	}

	cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", repoURL, destDir)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("git stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("git stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("git clone start: %w", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			log.Info(scanner.Text(), zap.String("stream", "git"))
		}
	}()

	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			log.Info(scanner.Text(), zap.String("stream", "git"))
		}
	}()

	// Wait for process to exit, then for goroutines to drain remaining output.
	waitErr := cmd.Wait()
	wg.Wait()

	if waitErr != nil {
		return fmt.Errorf("git clone: %w", waitErr)
	}
	return nil
}
