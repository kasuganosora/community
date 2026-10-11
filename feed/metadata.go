package feed

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// ScanTimelineMetadata keeps cursors local to each primary. visit may run
// concurrently for different primaries; each key identifies just one feed.
func ScanTimelineMetadata(ctx context.Context, client redis.UniversalClient, pattern string, visit func(string) error) error {
	scan := func(ctx context.Context, scanner redis.UniversalClient) error {
		var cursor uint64
		for {
			limited, cancel := context.WithTimeout(ctx, 5*time.Second)
			keys, next, err := scanner.Scan(limited, cursor, pattern, 256).Result()
			cancel()
			if err != nil {
				return err
			}
			for _, key := range keys {
				if err := visit(key); err != nil {
					return err
				}
			}
			cursor = next
			if cursor == 0 {
				return nil
			}
		}
	}
	if cluster, ok := client.(*redis.ClusterClient); ok {
		return cluster.ForEachMaster(ctx, func(ctx context.Context, node *redis.Client) error { return scan(ctx, node) })
	}
	return scan(ctx, client)
}
