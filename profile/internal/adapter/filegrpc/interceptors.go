package filegrpc

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
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
