package main

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sync"

	"go.uber.org/zap"
)

// GenerateBuild runs `railpack plan` against srcDir to detect the framework
// and log the build plan. Railpack v0.18+ is a BuildKit gateway frontend and
// does not produce a Dockerfile; the actual build is driven by the railpack
// frontend image inside BuildKit (see buildkit.go).
func GenerateBuild(ctx context.Context, srcDir string, log *zap.Logger) error {
	cmd := exec.CommandContext(ctx, "/usr/local/bin/railpack", "plan", "--out", filepath.Join(srcDir, "railpack-plan.json"), srcDir)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("railpack stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("railpack stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("railpack start: %w", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			log.Info(scanner.Text(), zap.String("stream", "railpack"))
		}
	}()

	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			log.Info(scanner.Text(), zap.String("stream", "railpack"))
		}
	}()

	waitErr := cmd.Wait()
	wg.Wait()

	if waitErr != nil {
		return fmt.Errorf("railpack plan: %w", waitErr)
	}

	return nil
}
