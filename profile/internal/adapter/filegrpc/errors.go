package filegrpc

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/a-aleesshin/gophprofile/profile/internal/usecase"
)

func translate(call string, err error) error {
	code := status.Code(err)

	switch code {
	case codes.InvalidArgument:
		return fmt.Errorf("%w: file service rejected %s: %s",
			usecase.ErrInvalidCommand, call, status.Convert(err).Message())

	case codes.NotFound:
		return fmt.Errorf("%w: %s: %s",
			usecase.ErrUploadNotFound, call, status.Convert(err).Message())

	case codes.FailedPrecondition:
		return fmt.Errorf("%w: %s: %s",
			usecase.ErrUploadNotUsable, call, status.Convert(err).Message())

	case codes.ResourceExhausted:
		return fmt.Errorf("%w: %s: %s",
			usecase.ErrFileTooLarge, call, status.Convert(err).Message())

	case codes.Unavailable:
		return fmt.Errorf("%w: %s: %s",
			usecase.ErrFileServiceUnavailable, call, status.Convert(err).Message())

	case codes.DeadlineExceeded:
		return fmt.Errorf("%w: %s: %s",
			usecase.ErrFileServiceTimeout, call, status.Convert(err).Message())

	case codes.Canceled:
		return fmt.Errorf("%s: %w", call, context.Canceled)

	default:
		return fmt.Errorf("%s: %s: %w", call, code.String(), err)
	}
}
