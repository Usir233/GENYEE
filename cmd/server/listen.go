package main

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/Usir233/GENYEE/internal/logger"
)

func listenWithRetry(addr string, maxRetries int, baseDelay time.Duration) (net.Listener, error) {
	if maxRetries <= 0 {
		return nil, errors.New("listenWithRetry: maxRetries must be > 0")
	}
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		listener, err := net.Listen("tcp", addr)
		if err == nil {
			return listener, nil
		}
		lastErr = err
		if i < maxRetries-1 {
			shift := min(i, 30)
			delay := baseDelay * time.Duration(1<<uint(shift))
			if delay > 3*time.Second {
				delay = 3 * time.Second
			}
			logger.Warnf(
				context.Background(),
				"Port %s in use, retrying in %v... (%d/%d)",
				addr, delay, i+1, maxRetries,
			)
			time.Sleep(delay)
		}
	}
	return nil, lastErr
}
