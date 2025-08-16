package fn

import (
	"fmt"
	"log/slog"
	"time"
)

func TimingMiddlewareLogging(phase string, log string) func() {
	start := time.Now()
	return func() {
		duration := time.Since(start)
		slog.Info(fmt.Sprintf("[%s] %s in %v",
			phase,
			log,
			duration,
		))
	}
}
