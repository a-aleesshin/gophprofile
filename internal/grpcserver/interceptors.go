package grpcserver

import (
	"context"
	"crypto/subtle"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/a-aleesshin/gophprofile/internal/metrics"
)

const (
	authorizationKey = "authorization"
	bearerPrefix     = "Bearer "

	healthPrefix = "/grpc.health.v1.Health/"
)

func exempt(method string) bool {
	return strings.HasPrefix(method, healthPrefix)
}

func reportPanic(logger *slog.Logger, method string, recovered any) error {
	logger.Error("a grpc handler panicked",
		"method", method,
		"panic", fmt.Sprint(recovered),
		"stack", string(debug.Stack()))

	return status.Error(codes.Internal, "internal error")
}

func verify(ctx context.Context, expected []byte) error {
	md, carried := metadata.FromIncomingContext(ctx)
	if !carried {
		return status.Error(codes.Unauthenticated, "no metadata")
	}

	values := md.Get(authorizationKey)
	if len(values) != 1 {
		return status.Error(codes.Unauthenticated, "no authorization token")
	}

	if subtle.ConstantTimeCompare([]byte(values[0]), expected) != 1 {
		return status.Error(codes.Unauthenticated, "the authorization token is wrong")
	}

	return nil
}

func observe(logger *slog.Logger, kind, method string, took time.Duration, err error) {
	code := status.Code(err).String()

	metrics.ObserveGRPCCall(kind, method, code, took)

	attrs := []any{
		"kind", kind,
		"method", method,
		"code", code,
		"took", took,
	}

	if err != nil {
		logger.Error("grpc call failed", append(attrs, "error", err.Error())...)

		return
	}

	logger.Debug("grpc call served", attrs...)
}

func recovery(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		request any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (response any, err error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				response, err = nil, reportPanic(logger, info.FullMethod, recovered)
			}
		}()

		return handler(ctx, request)
	}
}

func recoveryStream(logger *slog.Logger) grpc.StreamServerInterceptor {
	return func(
		service any,
		stream grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) (err error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				err = reportPanic(logger, info.FullMethod, recovered)
			}
		}()

		return handler(service, stream)
	}
}

func logging(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		request any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		if exempt(info.FullMethod) {
			return handler(ctx, request)
		}

		started := time.Now()

		response, err := handler(ctx, request)

		observe(logger, metrics.KindUnary, info.FullMethod, time.Since(started), err)

		return response, err
	}
}

func loggingStream(logger *slog.Logger) grpc.StreamServerInterceptor {
	return func(
		service any,
		stream grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		if exempt(info.FullMethod) {
			return handler(service, stream)
		}

		started := time.Now()

		err := handler(service, stream)

		observe(logger, metrics.KindStream, info.FullMethod, time.Since(started), err)

		return err
	}
}

func authorization(token string) grpc.UnaryServerInterceptor {
	expected := []byte(bearerPrefix + token)

	return func(
		ctx context.Context,
		request any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		if exempt(info.FullMethod) {
			return handler(ctx, request)
		}

		if err := verify(ctx, expected); err != nil {
			return nil, err
		}

		return handler(ctx, request)
	}
}

func authorizationStream(token string) grpc.StreamServerInterceptor {
	expected := []byte(bearerPrefix + token)

	return func(
		service any,
		stream grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		if exempt(info.FullMethod) {
			return handler(service, stream)
		}

		if err := verify(stream.Context(), expected); err != nil {
			return err
		}

		return handler(service, stream)
	}
}
