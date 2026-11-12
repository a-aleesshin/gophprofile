package grpc

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	filev1 "github.com/a-aleesshin/gophprofile/file/gen/file/v1"
	"github.com/a-aleesshin/gophprofile/file/internal/domain"
	"github.com/a-aleesshin/gophprofile/file/internal/usecase"
)

var protoStatusByDomain = map[string]filev1.FileStatus{
	domain.FileStatusReceived.String():   filev1.FileStatus_FILE_STATUS_RECEIVED,
	domain.FileStatusProcessing.String(): filev1.FileStatus_FILE_STATUS_PROCESSING,
	domain.FileStatusReady.String():      filev1.FileStatus_FILE_STATUS_READY,
	domain.FileStatusRejected.String():   filev1.FileStatus_FILE_STATUS_REJECTED,
	domain.FileStatusDeleting.String():   filev1.FileStatus_FILE_STATUS_DELETING,
	domain.FileStatusDeleted.String():    filev1.FileStatus_FILE_STATUS_DELETED,
}

func toProtoStatus(value domain.FileStatus) filev1.FileStatus {
	if mapped, ok := protoStatusByDomain[value.String()]; ok {
		return mapped
	}

	return filev1.FileStatus_FILE_STATUS_UNSPECIFIED
}

func toProtoFile(view usecase.FileView, variants []usecase.VariantView) *filev1.File {
	converted := make([]*filev1.Variant, 0, len(variants))

	for _, variant := range variants {
		converted = append(converted, &filev1.Variant{
			Name:      variant.Name,
			Ext:       variant.Ext,
			MimeType:  variant.MimeType,
			Width:     variant.Width,
			Height:    variant.Height,
			SizeBytes: variant.SizeBytes,
			Etag:      variant.ETag,
		})
	}

	return &filev1.File{
		Id:           view.ID.String(),
		Namespace:    view.Namespace.String(),
		OwnerRef:     view.OwnerRef,
		MimeType:     view.MimeType,
		SizeBytes:    view.SizeBytes,
		Width:        view.Width,
		Height:       view.Height,
		Status:       toProtoStatus(view.Status),
		Variants:     converted,
		CreatedAt:    timestamppb.New(view.CreatedAt),
		RejectReason: view.RejectReason,
	}
}

func toStatus(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, err.Error())

	case errors.Is(err, usecase.ErrInvalidCommand),
		errors.Is(err, usecase.ErrUnknownNamespace),
		errors.Is(err, usecase.ErrUnsupportedMimeType):
		return status.Error(codes.InvalidArgument, err.Error())

	case errors.Is(err, domain.ErrFileNotFound),
		errors.Is(err, domain.ErrUploadNotFound),
		errors.Is(err, domain.ErrVariantAbsent):
		return status.Error(codes.NotFound, err.Error())

	case errors.Is(err, usecase.ErrSizeExceeded):
		return status.Error(codes.ResourceExhausted, err.Error())

	case errors.Is(err, usecase.ErrUploadExpired),
		errors.Is(err, usecase.ErrUploadNotReusable),
		errors.Is(err, usecase.ErrObjectNotFound),
		errors.Is(err, usecase.ErrObjectUnusable),
		errors.Is(err, usecase.ErrFileNotServable),
		errors.Is(err, usecase.ErrStaleVersion):
		return status.Error(codes.FailedPrecondition, err.Error())

	case errors.Is(err, usecase.ErrStorageUnavailable):
		return status.Error(codes.Unavailable, err.Error())

	default:
		return status.Error(codes.Internal, err.Error())
	}
}
