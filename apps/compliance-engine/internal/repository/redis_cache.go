package repository

import (
	"context"
	"fmt"

	"omnipulse/apps/compliance-engine/internal/domain"

	"github.com/redis/go-redis/v9"
)

type RedisComplianceRepository struct {
	rdb *redis.Client
}

// NewRedisComplianceRepository instantiates the key-value storage driver wrapper
func NewRedisComplianceRepository(rdb *redis.Client) domain.ComplianceRepository {
	return &RedisComplianceRepository{rdb: rdb}
}

// Build standard Redis cache keys to avoid dirty namespace collisions
func (r *RedisComplianceRepository) makeKey(platform, routingValue string) string {
	return fmt.Sprintf("compliance:%s:%s", platform, routingValue)
}

func (r *RedisComplianceRepository) IsOptedOut(ctx context.Context, platform, routingValue string) (bool, error) {
	key := r.makeKey(platform, routingValue)

	// Check for string presence inside Redis cache lines
	val, err := r.rdb.Get(ctx, key).Result()
	if err == redis.Nil {
		return false, nil // Key does not exist, user is clean to message
	}
	if err != nil {
		return false, fmt.Errorf("redis lookup transaction execution crash: %w", err)
	}

	return val == "true", nil
}

func (r *RedisComplianceRepository) SetOptOutStatus(ctx context.Context, platform, routingValue string, optedOut bool) error {
	key := r.makeKey(platform, routingValue)

	if optedOut {
		// Store the blacklist flag. We set no expiration (0) because opt-outs are persistent until a user re-subscribes.
		err := r.rdb.Set(ctx, key, "true", 0).Err()
		if err != nil {
			return fmt.Errorf("failed to commit blacklist state to redis cache: %w", err)
		}
	} else {
		// If re-subscribing, drop the constraint line entirely out of memory
		err := r.rdb.Del(ctx, key).Err()
		if err != nil {
			return fmt.Errorf("failed to purge blacklist state from redis cache: %w", err)
		}
	}

	return nil
}
