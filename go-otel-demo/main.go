package main

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type ExporterType int

const (
	ExporterConsoleType = 0
	ExporterGRPCType    = 1
)

var (
	otelExporterType ExporterType = ExporterGRPCType
)

func setupLogger() *zap.Logger {
	config := zap.NewProductionConfig()
	// config.Encoding = "console"
	config.OutputPaths = []string{
		"stdout",
		"logs/app.log",
	}
	config.ErrorOutputPaths = []string{
		"stderr",
		"logs/app.log",
	}
	config.EncoderConfig.TimeKey = "timestamp"
	config.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	config.EncoderConfig.CallerKey = "caller"
	config.EncoderConfig.MessageKey = "msg"
	config.EncoderConfig.LevelKey = "level"

	logger := zap.Must(config.Build())
	return logger
}

func setupTracerProvider(ctx context.Context) (*sdktrace.TracerProvider, error) {
	var traceExp sdktrace.SpanExporter

	traceExp, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
	if otelExporterType == ExporterGRPCType {
		traceExp, err = otlptracegrpc.New(ctx,
			otlptracegrpc.WithInsecure(),
			// otlptracegrpc.WithEndpoint("localhost:4317"), // OTLP gRPC endpoint (Alloy or Tempo)
		)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create trace exporter: %w", err)
	}

	res, _ := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName("otel-demo"),
		),
	)

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExp),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(tp)
	return tp, nil
}

func setupMeterProvider(ctx context.Context) (*sdkmetric.MeterProvider, error) {
	var metricExp sdkmetric.Exporter

	metricExp, err := stdoutmetric.New()
	if otelExporterType == ExporterGRPCType {
		metricExp, err = otlpmetricgrpc.New(ctx,
			otlpmetricgrpc.WithInsecure(),
			// otlpmetricgrpc.WithEndpoint("localhost:4317"), // OTLP gRPC endpoint (Alloy)
		)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create metric exporter: %w", err)
	}

	res, _ := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName("otel-demo"),
		),
	)

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp)),
		sdkmetric.WithResource(res),
	)

	otel.SetMeterProvider(mp)
	return mp, nil
}

func main() {
	ctx := context.Background()
	logger := setupLogger()
	defer logger.Sync()

	tp, err := setupTracerProvider(ctx)
	if err != nil {
		logger.Fatal("Failed to init trace provider", zap.Error(err))
	}

	mp, err := setupMeterProvider(ctx)
	if err != nil {
		logger.Fatal("Failed to init metric provider", zap.Error(err))
	}

	tracer := otel.Tracer("otel-demo")
	meter := otel.Meter("otel-demo")

	// Create a metric counter
	requests, _ := meter.Int64Counter("http_requests_total")

	// Init http server and handler
	server := fiber.New()

	otelDemoHandler := func() fiber.Handler {
		return func(c *fiber.Ctx) error {
			ctx, span := tracer.Start(c.UserContext(), "handle-demo-request")
			defer span.End()

			logger.Info("Received request", zap.String("method", c.Method()), zap.String("url", c.Path()))
			requests.Add(ctx, 1, metric.WithAttributes(attribute.String("path", c.Path()), attribute.String("method", c.Method())))

			// Simulate work
			time.Sleep(time.Duration(rand.Intn(300)+100) * time.Millisecond)

			return c.Status(http.StatusOK).SendString("OpenTelemetry + Zap is working!")
		}
	}
	server.Get("/", otelDemoHandler())

	logger.Info("Server is running", zap.String("port", ":8080"))

	// Graceful shutdown
	go func() {
		if err := server.Listen(":8080"); err != nil {
			logger.Fatal("Server unable to run!", zap.Error(err))
		}
	}()

	gracefulCtx := context.Background()
	gracefulCtx, stop := signal.NotifyContext(gracefulCtx, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	defer stop()

	<-gracefulCtx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Duration(10*time.Second))
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(3)
	{
		go func(ctx context.Context) {
			defer wg.Done()
			if err := tp.Shutdown(ctx); err != nil {
				logger.Fatal("Tracer provider cannot shutting down properly!", zap.Error(err))
			}
		}(shutdownCtx)

		go func(ctx context.Context) {
			defer wg.Done()
			if err := mp.Shutdown(ctx); err != nil {
				logger.Fatal("Metrics provider cannot shutting down properly!", zap.Error(err))
			}
		}(shutdownCtx)

		go func(ctx context.Context) {
			defer wg.Done()
			if err := server.Shutdown(); err != nil {
				logger.Fatal("Server cannot shutting down properly!", zap.Error(err))
			}
		}(shutdownCtx)
	}
	wg.Wait()

	logger.Info("Server is shutting down!")
}
