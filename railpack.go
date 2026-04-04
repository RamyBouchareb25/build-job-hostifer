package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"go.uber.org/zap"
)

const (
	defaultRailpackVersion = "0.18.0"
	railpackPlanFileName   = "railpack-plan.json"
	railpackInfoFileName   = "railpack-info.json"
	maxRailpackOutputBytes = 4 * 1024
	defaultNodeMemoryMb    = 768
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
		if err := patchRailpackPlanRunAsUser(srcDir, log); err != nil {
			return err
		}

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

		if err := patchRailpackPlanRunAsUser(srcDir, log); err != nil {
			return err
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

func injectNodeMemoryFlag(cmd string, limitMb int) string {
	// Set Node heap to 80% of container limit so GC kicks in before OOM.
	heapMb := int(float64(limitMb) * 0.80)
	flag := fmt.Sprintf("--max-old-space-size=%d", heapMb)

	// Handle: node server.js -> node --max-old-space-size=204 server.js
	if strings.HasPrefix(cmd, "node ") {
		return strings.Replace(cmd, "node ", fmt.Sprintf("node %s ", flag), 1)
	}
	// Handle: npm start / npm run start -> NODE_OPTIONS=... npm start
	return fmt.Sprintf("NODE_OPTIONS='%s' %s", "--max-old-space-size="+strconv.Itoa(heapMb), cmd)
}

func nodeMemoryLimitMb() int {
	raw := strings.TrimSpace(os.Getenv("MEMORY_LIMIT_MB"))
	if raw == "" {
		return defaultNodeMemoryMb
	}

	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		return defaultNodeMemoryMb
	}

	return parsed
}

func patchRailpackPlanRunAsUser(srcDir string, log *zap.Logger) error {
	planPath := filepath.Join(srcDir, railpackPlanFileName)
	log.Info("patching railpack plan to run as uid 1000",
		zap.String("stream", "railpack"),
		zap.String("path", planPath),
	)

	planBytes, err := os.ReadFile(planPath)
	if err != nil {
		return fmt.Errorf("read railpack plan: %w", err)
	}

	var plan map[string]interface{}
	if err := json.Unmarshal(planBytes, &plan); err != nil {
		return fmt.Errorf("parse railpack plan: %w", err)
	}

	deploy, ok := plan["deploy"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("patch railpack plan: missing or invalid deploy section")
	}
	deploy["user"] = "1000"

	memoryLimitMb := nodeMemoryLimitMb()
	if startCmd, exists := deploy["startCommand"].(string); exists {
		// Inject memory limit slightly below the container limit so Node.js
		// manages its own heap before the kernel OOM killer hits.
		if strings.Contains(startCmd, "node") || strings.Contains(startCmd, "npm") || strings.Contains(startCmd, "yarn") {
			deploy["startCommand"] = injectNodeMemoryFlag(startCmd, memoryLimitMb)
		}
	}

	chownCmd := map[string]interface{}{
		"cmd":        "chown -R 1000:1000 /app",
		"customName": "set ownership to uid 1000",
	}

	steps, _ := plan["steps"].([]interface{})
	inserted := false

	// Prefer a dedicated build step when available.
	for _, s := range steps {
		step, ok := s.(map[string]interface{})
		if !ok {
			continue
		}

		name, _ := step["name"].(string)
		if name != "build" {
			continue
		}

		cmds, ok := step["commands"].([]interface{})
		if !ok {
			cmds = []interface{}{}
		}
		step["commands"] = append(cmds, chownCmd)
		inserted = true
		break
	}

	// If there is no build step, append to the last step that supports commands.
	if !inserted {
		for i := len(steps) - 1; i >= 0; i-- {
			step, ok := steps[i].(map[string]interface{})
			if !ok {
				continue
			}

			cmds, ok := step["commands"].([]interface{})
			if !ok {
				continue
			}

			step["commands"] = append(cmds, chownCmd)
			inserted = true
			break
		}
	}

	// Final fallback for plans without a conventional command-bearing step.
	if !inserted {
		steps = append(steps, map[string]interface{}{
			"name":     "hostifer-permissions",
			"commands": []interface{}{chownCmd},
		})
		plan["steps"] = steps
	}

	patched, err := json.Marshal(plan)
	if err != nil {
		return fmt.Errorf("marshal patched plan: %w", err)
	}

	if err := os.WriteFile(planPath, patched, 0644); err != nil {
		return fmt.Errorf("write patched plan: %w", err)
	}

	log.Info("patched railpack plan",
		zap.String("stream", "railpack"),
		zap.String("path", planPath),
	)

	return nil
}
