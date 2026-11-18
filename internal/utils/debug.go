package utils

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// CleanupStaleRunningTasks scans and removes stale running-task Redis keys
// matching keyPrefix* without blocking Redis via KEYS.
func CleanupStaleRunningTasks(
	ctx context.Context,
	redisClient *redis.Client,
	keyPrefix string,
	maxAge time.Duration,
) (int, error) {
	if redisClient == nil {
		return 0, errors.New("redis client is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	var keys []string
	var cursor uint64
	for {
		batch, nextCursor, err := redisClient.Scan(ctx, cursor, keyPrefix+"*", 100).Result()
		if err != nil {
			return 0, fmt.Errorf("failed to scan keys: %w", err)
		}
		keys = append(keys, batch...)
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	if len(keys) == 0 {
		return 0, nil
	}

	var staleTasks []string
	for _, key := range keys {
		ttl, err := redisClient.TTL(ctx, key).Result()
		if err != nil {
			continue
		}
		// Persistent keys (ttl == -1) or keys whose remaining TTL exceeds maxAge are stale;
		// ttl == -2 means the key disappeared concurrently.
		if ttl == -1 || ttl > maxAge {
			staleTasks = append(staleTasks, key)
		}
	}

	if len(staleTasks) == 0 {
		return 0, nil
	}

	deleted, err := redisClient.Del(ctx, staleTasks...).Result()
	if err != nil {
		return 0, fmt.Errorf("failed to delete stale keys: %w", err)
	}

	return int(deleted), nil
}

// CheckRunningTaskStatus inspects the existence and TTL of a running-task key
// and its associated progress key.
func CheckRunningTaskStatus(
	ctx context.Context,
	redisClient *redis.Client,
	runningKey, progressKey string,
) (map[string]interface{}, error) {
	if redisClient == nil {
		return nil, errors.New("redis client is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	result := make(map[string]interface{})

	runningTaskID, err := redisClient.Get(ctx, runningKey).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			result["running_task_exists"] = false
		} else {
			return nil, fmt.Errorf("failed to get running task: %w", err)
		}
	} else {
		result["running_task_exists"] = true
		result["running_task_id"] = runningTaskID

		ttl, ttlErr := redisClient.TTL(ctx, runningKey).Result()
		if ttlErr != nil {
			return nil, fmt.Errorf("failed to get running task TTL: %w", ttlErr)
		}
		result["running_task_ttl"] = ttl.String()
	}

	progressData, err := redisClient.Get(ctx, progressKey).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			result["progress_exists"] = false
		} else {
			return nil, fmt.Errorf("failed to get progress: %w", err)
		}
	} else {
		result["progress_exists"] = true
		result["progress_data"] = progressData

		ttl, ttlErr := redisClient.TTL(ctx, progressKey).Result()
		if ttlErr != nil {
			return nil, fmt.Errorf("failed to get progress TTL: %w", ttlErr)
		}
		result["progress_ttl"] = ttl.String()
	}

	return result, nil
}
