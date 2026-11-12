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

	"github.com/a-aleesshin/gophprofile/internal/metrics"
	"google.golang.org/grpc/status"
)

const (
	authorizationKey = "authorization"
	bearerPrefix     = "Bearer "

	healthPrefix = "/grpc.health.v1.Health/"
)

func exempt(method string) bool {
	return strings.HasPrefix(method, healthPrefix)
}

func recovery(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		request any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (response any, err error) {
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}

			logger.Error("a grpc handler panicked",
				"method", info.FullMethod,
				"panic", fmt.Sprint(recovered),
				"stack", string(debug.Stack()))

			response = nil
			err = status.Error(codes.Internal, "internal error")
		}()

		return handler(ctx, request)
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

		took := time.Since(started)
		code := status.Code(err).String()

		metrics.ObserveGRPCCall(info.FullMethod, code, took)

		attrs := []any{
			"method", info.FullMethod,
			"code", code,
			"took", took,
		}

		if err != nil {
			logger.Error("grpc call failed", append(attrs, "error", err.Error())...)

			return response, err
		}

		logger.Debug("grpc call served", attrs...)

		return response, nil
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

		md, carried := metadata.FromIncomingContext(ctx)
		if !carried {
			return nil, status.Error(codes.Unauthenticated, "no metadata")
		}

		values := md.Get(authorizationKey)
		if len(values) != 1 {
			return nil, status.Error(codes.Unauthenticated, "no authorization token")
		}

		if subtle.ConstantTimeCompare([]byte(values[0]), expected) != 1 {
			return nil, status.Error(codes.Unauthenticated, "the authorization token is wrong")
		}

		return handler(ctx, request)
	}
}
