package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"go.uber.org/zap"
)

func main() {
	// 1. Read and validate all environment variables.
	repoURL, err := requireEnv("REPO_URL")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	imageName, err := requireEnv("IMAGE_NAME")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	buildkitHost, err := requireEnv("BUILDKIT_HOST")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	buildID, err := requireEnv("BUILD_ID")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	tenantID, err := requireEnv("TENANT_ID")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	workDir := os.Getenv("WORK_DIR")
	if workDir == "" {
		workDir = "/tmp/build"
	}

	// 2. Initialize structured logger — all subsequent output is JSON.
	log := NewLogger(buildID, tenantID)
	defer log.Sync() //nolint:errcheck

	// Single context covering the entire run.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	// 3. Build started.
	log.Info("build started",
		zap.String("repo", repoURL),
		zap.String("image", imageName),
	)

	// 4. Clone.
	if err := CloneRepo(ctx, repoURL, workDir, log); err != nil {
		log.Fatal("clone failed", zap.Error(err))
	}

	// 5. Clone complete.
	log.Info("clone complete")

	// 6. Framework detection & Dockerfile generation.
	if err := GenerateBuild(ctx, workDir, log); err != nil {
		log.Fatal("railpack failed", zap.Error(err))
	}

	// 7. Framework detection complete.
	log.Info("framework detected")

	// 8. Build & push.
	if err := BuildAndPush(ctx, buildkitHost, workDir, imageName, log); err != nil {
		log.Fatal("build and push failed", zap.Error(err))
	}

	// 9. Success.
	log.Info("build complete", zap.String("image", imageName))

	// 10. Exit 0 (implicit).
}

// requireEnv returns the value of the named environment variable or an error
// if the variable is absent or empty.
func requireEnv(name string) (string, error) {
	v := os.Getenv(name)
	if v == "" {
		return "", fmt.Errorf("required environment variable %q is not set", name)
	}
	return v, nil
}
