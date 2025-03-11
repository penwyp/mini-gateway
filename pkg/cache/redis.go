package cache

import (
	"context"

	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

var Client *redis.Client

func Init(cfg *config.Config) {
	Client = redis.NewClient(&redis.Options{
		Addr:     cfg.Cache.Addr,     // e.g., "localhost:6379"
		Password: cfg.Cache.Password, // 如果有密码
		DB:       cfg.Cache.DB,       // 默认 0
	})

	_, err := Client.Ping(context.Background()).Result()
	if err != nil {
		logger.Error("Failed to connect to Redis", zap.Error(err))
		panic(err)
	}
	logger.Info("Redis connected", zap.String("addr", cfg.Cache.Addr))
}
