package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
)

const (
	defaultRailpackVersion = "0.18.0"
	railpackPlanFileName   = "railpack-plan.json"
	railpackInfoFileName   = "railpack-info.json"
	maxRailpackOutputBytes = 4 * 1024
)

func railpackVersion() string {
	version := os.Getenv("RAILPACK_VERSION")
	if version == "" {
		return defaultRailpackVersion
	}

	return version
}

func railpackFrontendImage() string {
	return fmt.Sprintf("ghcr.io/railwayapp/railpack-frontend:v%s", railpackVersion())
}

// GenerateBuild runs `railpack prepare` against srcDir to generate the
// railpack plan consumed by the BuildKit frontend. The plan and info files are
// written into the build context so BuildKit can load them from the dockerfile
// local mount.
func GenerateBuild(ctx context.Context, srcDir string, log *zap.Logger) error {
	railpackPath, err := exec.LookPath("railpack")
	if err != nil {
		return fmt.Errorf("railpack not found in PATH=%q: %w", os.Getenv("PATH"), err)
	}

	args := []string{
		"prepare",
		"--plan-out", filepath.Join(srcDir, railpackPlanFileName),
		"--info-out", filepath.Join(srcDir, railpackInfoFileName),
		"--hide-pretty-plan",
		srcDir,
	}

	err = runRailpack(ctx, railpackPath, args, log)
	if err == nil {
		return nil
	}

	if strings.Contains(err.Error(), "--hide-pretty-plan") {
		log.Warn("railpack does not support --hide-pretty-plan, retrying without it",
			zap.String("stream", "railpack"),
		)

		fallbackArgs := []string{
			"prepare",
			"--plan-out", filepath.Join(srcDir, railpackPlanFileName),
			"--info-out", filepath.Join(srcDir, railpackInfoFileName),
			srcDir,
		}

		if fallbackErr := runRailpack(ctx, railpackPath, fallbackArgs, log); fallbackErr != nil {
			return fallbackErr
		}

		return nil
	}

	return err
}

func runRailpack(ctx context.Context, railpackPath string, args []string, log *zap.Logger) error {
	cmd := exec.CommandContext(ctx, railpackPath, args...)

	output, err := cmd.CombinedOutput()
	logRailpackOutput(output, log)

	if err != nil {
		trimmedOutput := strings.TrimSpace(string(output))
		if trimmedOutput == "" {
			return fmt.Errorf("railpack prepare: %w", err)
		}

		if len(trimmedOutput) > maxRailpackOutputBytes {
			trimmedOutput = trimmedOutput[:maxRailpackOutputBytes] + "..."
		}

		return fmt.Errorf("railpack prepare: %w: %s", err, trimmedOutput)
	}

	return nil
}

func logRailpackOutput(output []byte, log *zap.Logger) {
	if len(output) == 0 {
		return
	}

	scanner := bufio.NewScanner(bytes.NewReader(output))
	// Railpack occasionally emits very long lines; increase the scanner token cap.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		log.Info(text, zap.String("stream", "railpack"))
	}

	if err := scanner.Err(); err != nil {
		log.Warn("failed reading railpack output",
			zap.String("stream", "railpack"),
			zap.Error(err),
		)
	}
}
