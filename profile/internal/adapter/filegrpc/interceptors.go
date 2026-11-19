package filegrpc

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/a-aleesshin/gophprofile/internal/breaker"
)

func authorize(token string) grpc.UnaryClientInterceptor {
	value := bearerPrefix + token

	return func(
		ctx context.Context,
		method string,
		request, response any,
		conn *grpc.ClientConn,
		invoke grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		ctx = metadata.AppendToOutgoingContext(ctx, authorizationKey, value)

		return invoke(ctx, method, request, response, conn, opts...)
	}
}

func deadline(timeout time.Duration) grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		request, response any,
		conn *grpc.ClientConn,
		invoke grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		return invoke(ctx, method, request, response, conn, opts...)
	}
}

func protect(guard *breaker.Breaker) grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		request, response any,
		conn *grpc.ClientConn,
		invoke grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		if err := guard.Allow(); err != nil {
			return status.Error(codes.Unavailable, err.Error())
		}

		err := invoke(ctx, method, request, response, conn, opts...)
		guard.Record(brokenCall(err))

		return err
	}
}

func brokenCall(err error) bool {
	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded:
		return true
	default:
		return false
	}
}
