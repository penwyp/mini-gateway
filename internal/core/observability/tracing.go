package observability

import (
	"context"

	"github.com/penwyp/mini-gateway/config"
	"github.com/penwyp/mini-gateway/pkg/logger"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
	"go.uber.org/zap"
)

// InitTracing 初始化分布式追踪
func InitTracing(cfg *config.Config) func(context.Context) error {
	if !cfg.Observability.Jaeger.Enabled {
		logger.Info("Jaeger tracing is disabled")
		return func(ctx context.Context) error { return nil }
	} else {
		logger.Info("Jaeger tracing is enabled")
	}

	// 创建 OTLP HTTP 导出器
	exporter, err := otlptracehttp.New(context.Background(),
		otlptracehttp.WithEndpoint(cfg.Observability.Jaeger.Endpoint),
		otlptracehttp.WithURLPath("/v1/traces"),
		otlptracehttp.WithInsecure(), // 本地测试禁用 TLS，生产环境需配置 TLS
	)
	if err != nil {
		logger.Error("Failed to create OTLP exporter", zap.Error(err))
		panic(err)
	}

	// 配置采样器
	var sampler sdktrace.Sampler
	switch cfg.Observability.Jaeger.Sampler {
	case "always":
		sampler = sdktrace.AlwaysSample()
	case "ratio":
		sampler = sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.Observability.Jaeger.SampleRatio))
	default:
		sampler = sdktrace.AlwaysSample() // 默认全采样
		logger.Warn("Unknown sampler type, defaulting to always", zap.String("sampler", cfg.Observability.Jaeger.Sampler))
	}

	// 定义服务资源信息
	res, err := resource.New(context.Background(),
		resource.WithAttributes(
			semconv.ServiceNameKey.String("mini-gateway"),
			semconv.ServiceVersionKey.String("0.1.0"), // 可从 main.Version 获取
		),
	)
	if err != nil {
		logger.Error("Failed to create resource", zap.Error(err))
		panic(err)
	}

	// 创建 TracerProvider
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	)

	// 设置全局 TracerProvider 和 Propagator
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	logger.Info("Tracing initialized",
		zap.String("endpoint", cfg.Observability.Jaeger.Endpoint),
		zap.String("sampler", cfg.Observability.Jaeger.Sampler))

	return tp.Shutdown
}
