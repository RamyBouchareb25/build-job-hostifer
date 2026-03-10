package main

import (
	"go.uber.org/zap"
)

// NewLogger returns a production zap logger with build_id and tenant_id pre-attached
// to every log line. The production encoder writes newline-delimited JSON, which is
// exactly what the Next.js log-streaming backend expects.
func NewLogger(buildID, tenantID string) *zap.Logger {
	logger, err := zap.NewProduction()
	if err != nil {
		panic(err)
	}
	return logger.With(
		zap.String("build_id", buildID),
		zap.String("tenant_id", tenantID),
	)
}
