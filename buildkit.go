package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/docker/cli/cli/config"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/auth/authprovider"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

// BuildAndPush connects to the buildkit daemon, builds the image using the
// railpack BuildKit gateway frontend, and pushes it to imageName.
//
// Railpack v0.18+ works as a BuildKit gateway frontend — it does NOT produce a
// Dockerfile. BuildKit pulls ghcr.io/railwayapp/railpack as the frontend image
// and passes the source context to it directly.
//
// The registry is treated as insecure (HTTP). The buildkitd must also allow
// insecure pushes for the registry host in its buildkitd.toml.
func BuildAndPush(ctx context.Context, buildkitHost, contextDir, imageName string, log *zap.Logger) error {
	// BuildKit's gRPC client needs tcp:// (not http://).
	buildkitHost = strings.Replace(buildkitHost, "http://", "tcp://", 1)

	// Extract registry hostname, e.g. "registry.hostifer-system.svc.cluster.local:5000"
	registryHost := strings.SplitN(imageName, "/", 2)[0]

	// Connect with a hard 30-second deadline so we fail fast if the daemon is unreachable.
	connectCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// grpc.WithBlock causes client.New to block until the connection is actually
	// established. The connectCtx deadline (30 s) caps the wait.
	// buildkit's client.New already sets insecure credentials for tcp:// addresses.
	c, err := client.New(connectCtx, buildkitHost,
		client.WithGRPCDialOption(grpc.WithBlock()),
	)
	if err != nil {
		return fmt.Errorf("buildkit connect to %s: %w", buildkitHost, err)
	}
	defer c.Close()

	ch := make(chan *client.SolveStatus)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for status := range ch {
			for _, v := range status.Vertexes {
				if v.Name != "" {
					log.Info(v.Name, zap.String("stream", "buildkit"))
				}
			}
		}
	}()

	solveOpt := client.SolveOpt{
		// Forward host Docker credentials so BuildKit can push.
		Session: []session.Attachable{
			authprovider.NewDockerAuthProvider(config.LoadDefaultConfigFile(os.Stderr), nil),
		},
		// gateway.v0 tells buildkit to fetch the frontend image and delegate to it.
		// "source" is the frontend image; buildkit pulls it and runs it as an LLB frontend.
		Frontend: "gateway.v0",
		FrontendAttrs: map[string]string{
			"source": "ghcr.io/railwayapp/railpack-frontend:v0.18.0",
		},
		LocalDirs: map[string]string{
			"dockerfile": contextDir,
			"context":    contextDir,
		},
		Exports: []client.ExportEntry{
			{
				Type: client.ExporterImage,
				Attrs: map[string]string{
					"name": imageName,
					"push": "true",
					// Allow the daemon to push to the HTTP-only in-cluster registry.
					// Requires buildkitd to have the host in its insecure-registries list
					// OR the daemon to accept the registry.insecure attribute.
					"registry.insecure": "true",
				},
			},
		},
	}

	// Log which registry we are targeting so the Next.js backend can correlate.
	log.Info("pushing image",
		zap.String("stream", "buildkit"),
		zap.String("registry", registryHost),
		zap.String("image", imageName),
	)

	_, err = c.Solve(ctx, nil, solveOpt, ch)
	// Wait for the status goroutine to drain before reporting the result.
	wg.Wait()
	if err != nil {
		return fmt.Errorf("buildkit solve: %w", err)
	}

	return nil
}
