package filegrpc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	filev1 "github.com/a-aleesshin/gophprofile/file/gen/file/v1"
	"github.com/a-aleesshin/gophprofile/profile/internal/usecase"
)

var ErrInvalidConfig = errors.New("invalid file client config")

const (
	minTokenLength = 32

	authorizationKey = "authorization"
	bearerPrefix     = "Bearer "
)

const retryPolicy = `{
	"methodConfig": [{
		"name": [{"service": "gophprofile.file.v1.FileService"}],
		"retryPolicy": {
			"maxAttempts": 3,
			"initialBackoff": "0.1s",
			"maxBackoff": "1s",
			"backoffMultiplier": 2,
			"retryableStatusCodes": ["UNAVAILABLE"]
		}
	}]
}`

type Config struct {
	Addr    string        `env:"ADDR,required,notEmpty"`
	Token   string        `env:"TOKEN,required,notEmpty"`
	Timeout time.Duration `env:"TIMEOUT" envDefault:"5s"`
}

type Client struct {
	files   filev1.FileServiceClient
	close   func() error
	timeout time.Duration
}

func New(cfg Config) (*Client, error) {
	switch {
	case cfg.Addr == "":
		return nil, fmt.Errorf("%w: address is required", ErrInvalidConfig)
	case len(cfg.Token) < minTokenLength:
		return nil, fmt.Errorf("%w: token must be at least %d bytes",
			ErrInvalidConfig, minTokenLength)
	case cfg.Timeout <= 0:
		return nil, fmt.Errorf("%w: timeout must be positive", ErrInvalidConfig)
	}

	conn, err := grpc.NewClient(cfg.Addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultServiceConfig(retryPolicy),
		grpc.WithChainUnaryInterceptor(authorize(cfg.Token), deadline(cfg.Timeout)),
	)
	if err != nil {
		return nil, fmt.Errorf("file client: dial %s: %w", cfg.Addr, err)
	}

	return &Client{
		files:   filev1.NewFileServiceClient(conn),
		close:   conn.Close,
		timeout: cfg.Timeout,
	}, nil
}

func NewWithConn(conn grpc.ClientConnInterface, timeout time.Duration) (*Client, error) {
	switch {
	case conn == nil:
		return nil, fmt.Errorf("%w: connection is required", ErrInvalidConfig)
	case timeout <= 0:
		return nil, fmt.Errorf("%w: timeout must be positive", ErrInvalidConfig)
	}

	return &Client{files: filev1.NewFileServiceClient(conn), timeout: timeout}, nil
}

func (c *Client) Close() error {
	if c.close == nil {
		return nil
	}

	return c.close()
}

func (c *Client) CreateUpload(
	ctx context.Context,
	params usecase.CreateUploadParams,
) (usecase.CreateUploadTicket, error) {
	response, err := c.files.CreateUpload(ctx, &filev1.CreateUploadRequest{
		Namespace:      params.Namespace,
		OwnerRef:       params.OwnerRef,
		OriginalName:   params.OriginalName,
		DeclaredMime:   params.DeclaredMime,
		MaxSizeBytes:   params.MaxSizeBytes,
		IdempotencyKey: params.IdempotencyKey,
	})
	if err != nil {
		return usecase.CreateUploadTicket{}, translate("create upload", err)
	}

	switch {
	case response.GetUploadId() == "":
		return usecase.CreateUploadTicket{}, fmt.Errorf(
			"%w: create upload returned no upload id", usecase.ErrUploadNotUsable)
	case response.GetUrl() == "":
		return usecase.CreateUploadTicket{}, fmt.Errorf(
			"%w: create upload returned no url", usecase.ErrUploadNotUsable)
	case !response.GetExpiresAt().IsValid():
		return usecase.CreateUploadTicket{}, fmt.Errorf(
			"%w: create upload returned no expiry", usecase.ErrUploadNotUsable)
	}

	return usecase.CreateUploadTicket{
		UploadID:  response.GetUploadId(),
		URL:       response.GetUrl(),
		Fields:    response.GetFields(),
		ExpiresAt: response.GetExpiresAt().AsTime(),
	}, nil
}

func (c *Client) CompleteUpload(
	ctx context.Context,
	uploadID string,
) (usecase.CompleteUploadConfirmation, error) {
	if uploadID == "" {
		return usecase.CompleteUploadConfirmation{}, fmt.Errorf(
			"%w: upload id is required", usecase.ErrInvalidCommand)
	}

	response, err := c.files.CompleteUpload(ctx, &filev1.CompleteUploadRequest{
		UploadId: uploadID,
	})
	if err != nil {
		return usecase.CompleteUploadConfirmation{}, translate("complete upload", err)
	}

	if response.GetFileId() == "" {
		return usecase.CompleteUploadConfirmation{}, fmt.Errorf(
			"%w: complete upload returned no file id", usecase.ErrUploadNotUsable)
	}

	return usecase.CompleteUploadConfirmation{FileID: response.GetFileId()}, nil
}
