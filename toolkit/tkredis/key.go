package tkredis

import (
	"context"
	"strings"

	"github.com/redis/go-redis/v9"
)

// DeletePrefix 扫描并删除字面量前缀匹配的键；Cluster 遍历各主节点。
// 非事务快照，不保证覆盖并发新增的键，不关闭客户端。
func (c *Client) DeletePrefix(ctx context.Context, prefix string) (err error) {
	err = c.checkOpen()
	if err != nil {
		return
	}
	if prefix == "" {
		return invalidArgument("key prefix is empty")
	}
	ctx = nonNilContext(ctx)
	if cluster, ok := c.native.(*redis.ClusterClient); ok {
		return cluster.ForEachMaster(ctx, func(ctx context.Context, client *redis.Client) (err error) {
			return deletePrefixNode(ctx, client, prefix)
		})
	}
	return deletePrefixNode(ctx, c.native, prefix)
}

func deletePrefixNode(ctx context.Context, client redis.UniversalClient, prefix string) (err error) {
	pattern := strings.NewReplacer("\\", "\\\\", "*", "\\*", "?", "\\?", "[", "\\[", "]", "\\]").Replace(prefix) + "*"
	var cursor uint64
	for {
		err = ctx.Err()
		if err != nil {
			return
		}
		var keys []string
		keys, cursor, err = client.Scan(ctx, cursor, pattern, 128).Result()
		if err != nil {
			return
		}
		if len(keys) > 0 {
			pipeline := client.Pipeline()
			for _, key := range keys {
				if strings.HasPrefix(key, prefix) {
					pipeline.Del(ctx, key)
				}
			}
			_, err = pipeline.Exec(ctx)
			if err != nil {
				return
			}
		}
		if cursor == 0 {
			return
		}
	}
}
