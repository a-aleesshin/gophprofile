package grpc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	filev1 "github.com/a-aleesshin/gophprofile/file/gen/file/v1"
	"github.com/a-aleesshin/gophprofile/file/internal/domain"
	"github.com/a-aleesshin/gophprofile/file/internal/usecase"
)

var ErrIncompleteServer = errors.New("incomplete grpc server")

type uploadCreator interface {
	Handle(
		ctx context.Context,
		cmd usecase.CreateUploadCommand,
	) (usecase.CreateUploadResult, error)
}

type uploadCompleter interface {
	Handle(
		ctx context.Context,
		cmd usecase.CompleteUploadCommand,
	) (usecase.CompleteUploadResult, error)
}

type fileReader interface {
	Handle(ctx context.Context, cmd usecase.GetFileCommand) (usecase.GetFileResult, error)
}

type filesReader interface {
	Handle(ctx context.Context, cmd usecase.GetFilesCommand) (usecase.GetFilesResult, error)
}

type linkCreator interface {
	Handle(
		ctx context.Context,
		cmd usecase.CreateAccessLinkCommand,
	) (usecase.CreateAccessLinkResult, error)
}

type fileDeleter interface {
	Handle(ctx context.Context, cmd usecase.DeleteFileCommand) (usecase.DeleteFileResult, error)
}

type Handlers struct {
	CreateUpload     uploadCreator
	CompleteUpload   uploadCompleter
	GetFile          fileReader
	GetFiles         filesReader
	CreateAccessLink linkCreator
	DeleteFile       fileDeleter
}

type Server struct {
	filev1.UnimplementedFileServiceServer

	handlers Handlers
}

func NewServer(handlers Handlers) (*Server, error) {
	missing := map[string]bool{
		"CreateUpload":     handlers.CreateUpload == nil,
		"CompleteUpload":   handlers.CompleteUpload == nil,
		"GetFile":          handlers.GetFile == nil,
		"GetFiles":         handlers.GetFiles == nil,
		"CreateAccessLink": handlers.CreateAccessLink == nil,
		"DeleteFile":       handlers.DeleteFile == nil,
	}

	for name, absent := range missing {
		if absent {
			return nil, fmt.Errorf("%w: no handler for %s", ErrIncompleteServer, name)
		}
	}

	return &Server{handlers: handlers}, nil
}

func (s *Server) CreateUpload(
	ctx context.Context,
	request *filev1.CreateUploadRequest,
) (*filev1.CreateUploadResponse, error) {
	namespace, err := domain.NewNamespace(request.GetNamespace())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	ownerRef, err := domain.NewOwnerRef(request.GetOwnerRef())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	originalName, err := domain.NewOriginalName(request.GetOriginalName())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	idempotencyKey, err := domain.NewIdempotencyKey(request.GetIdempotencyKey())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	cmd := usecase.CreateUploadCommand{
		Namespace:      namespace,
		OwnerRef:       ownerRef,
		OriginalName:   originalName,
		IdempotencyKey: idempotencyKey,
	}

	if raw := request.GetDeclaredMime(); raw != "" {
		cmd.DeclaredMime, err = domain.NewMimeType(raw)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
	}

	if raw := request.GetMaxSizeBytes(); raw > 0 {
		cmd.MaxSize, err = domain.NewSize(raw)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
	}

	result, err := s.handlers.CreateUpload.Handle(ctx, cmd)
	if err != nil {
		return nil, toStatus(err)
	}

	return &filev1.CreateUploadResponse{
		UploadId:  result.UploadID.String(),
		Url:       result.URL,
		Fields:    result.Fields,
		ExpiresAt: timestamppb.New(result.ExpiresAt),
	}, nil
}

func (s *Server) CompleteUpload(
	ctx context.Context,
	request *filev1.CompleteUploadRequest,
) (*filev1.CompleteUploadResponse, error) {
	uploadID, err := domain.ParseUploadID(request.GetUploadId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	result, err := s.handlers.CompleteUpload.Handle(ctx, usecase.CompleteUploadCommand{
		UploadID: uploadID,
	})
	if err != nil {
		return nil, toStatus(err)
	}

	return &filev1.CompleteUploadResponse{
		FileId: result.FileID.String(),
		Status: toProtoStatus(result.Status),
	}, nil
}

func (s *Server) GetFile(
	ctx context.Context,
	request *filev1.GetFileRequest,
) (*filev1.GetFileResponse, error) {
	fileID, err := domain.ParseFileID(request.GetFileId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	result, err := s.handlers.GetFile.Handle(ctx, usecase.GetFileCommand{FileID: fileID})
	if err != nil {
		return nil, toStatus(err)
	}

	return &filev1.GetFileResponse{File: toProtoFile(result.File, result.Variants)}, nil
}

func (s *Server) GetFiles(
	ctx context.Context,
	request *filev1.GetFilesRequest,
) (*filev1.GetFilesResponse, error) {
	ids := make([]domain.FileID, 0, len(request.GetFileIds()))

	for _, raw := range request.GetFileIds() {
		fileID, err := domain.ParseFileID(raw)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}

		ids = append(ids, fileID)
	}

	result, err := s.handlers.GetFiles.Handle(ctx, usecase.GetFilesCommand{FileIDs: ids})
	if err != nil {
		return nil, toStatus(err)
	}

	files := make([]*filev1.File, 0, len(result.Files))

	for _, found := range result.Files {
		files = append(files, toProtoFile(found.File, found.Variants))
	}

	return &filev1.GetFilesResponse{Files: files}, nil
}

func (s *Server) CreateAccessLink(
	ctx context.Context,
	request *filev1.CreateAccessLinkRequest,
) (*filev1.CreateAccessLinkResponse, error) {
	fileID, err := domain.ParseFileID(request.GetFileId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	variant, err := domain.NewVariantName(request.GetVariant())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	if request.GetTtlSeconds() < 0 {
		return nil, status.Error(codes.InvalidArgument, "ttl_seconds must not be negative")
	}

	result, err := s.handlers.CreateAccessLink.Handle(ctx, usecase.CreateAccessLinkCommand{
		FileID:  fileID,
		Variant: variant,
		TTL:     time.Duration(request.GetTtlSeconds()) * time.Second,
	})
	if err != nil {
		return nil, toStatus(err)
	}

	response := &filev1.CreateAccessLinkResponse{Url: result.URL}

	if !result.ExpiresAt.IsZero() {
		response.ExpiresAt = timestamppb.New(result.ExpiresAt)
	}

	return response, nil
}

func (s *Server) DeleteFile(
	ctx context.Context,
	request *filev1.DeleteFileRequest,
) (*filev1.DeleteFileResponse, error) {
	fileID, err := domain.ParseFileID(request.GetFileId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	result, err := s.handlers.DeleteFile.Handle(ctx, usecase.DeleteFileCommand{FileID: fileID})
	if err != nil {
		return nil, toStatus(err)
	}

	return &filev1.DeleteFileResponse{Status: toProtoStatus(result.Status)}, nil
}
