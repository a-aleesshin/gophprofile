package s3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/a-aleesshin/gophprofile/file/internal/domain"
	"github.com/a-aleesshin/gophprofile/file/internal/usecase"
)

var ErrInTransaction = errors.New("object io must not run inside a database transaction")

var ErrInvalidConfig = errors.New("invalid s3 config")

type Config struct {
	Endpoint   string `env:"ENDPOINT,required,notEmpty"`
	PublicBase string `env:"PUBLIC_BASE,required,notEmpty"`
	AccessKey  string `env:"ACCESS_KEY,required,notEmpty"`
	SecretKey  string `env:"SECRET_KEY,required,notEmpty"`
	Bucket     string `env:"BUCKET,required,notEmpty"`
	Region     string `env:"REGION" envDefault:"us-east-1"`
	UseSSL     bool   `env:"USE_SSL" envDefault:"false"`
}

type TransactionGuard interface {
	InTransaction(ctx context.Context) bool
}

type Store struct {
	client     *minio.Client
	bucket     string
	guard      TransactionGuard
	publicBase string
}

func New(cfg Config, guard TransactionGuard) (*Store, error) {
	if guard == nil {
		return nil, fmt.Errorf("%w: transaction guard is required", ErrInvalidConfig)
	}

	switch {
	case cfg.Endpoint == "":
		return nil, fmt.Errorf("%w: endpoint is required", ErrInvalidConfig)
	case cfg.PublicBase == "":
		return nil, fmt.Errorf("%w: public base is required", ErrInvalidConfig)
	case cfg.Bucket == "":
		return nil, fmt.Errorf("%w: bucket is required", ErrInvalidConfig)
	case cfg.AccessKey == "" || cfg.SecretKey == "":
		return nil, fmt.Errorf("%w: credentials are required", ErrInvalidConfig)
	}

	if _, err := url.Parse(cfg.PublicBase); err != nil {
		return nil, fmt.Errorf("%w: public base %q: %w", ErrInvalidConfig, cfg.PublicBase, err)
	}

	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("new minio client: %w", err)
	}

	return &Store{
		client:     client,
		bucket:     cfg.Bucket,
		publicBase: cfg.PublicBase,
		guard:      guard,
	}, nil
}

func (s *Store) PresignUpload(
	ctx context.Context,
	params usecase.PresignUploadParams,
) (usecase.PresignedUpload, error) {
	policy := minio.NewPostPolicy()

	if err := policy.SetBucket(s.bucket); err != nil {
		return usecase.PresignedUpload{}, fmt.Errorf("set bucket: %w", err)
	}

	if err := policy.SetKey(params.Location.String()); err != nil {
		return usecase.PresignedUpload{}, fmt.Errorf("set key: %w", err)
	}

	if err := policy.SetExpires(params.ExpiresAt); err != nil {
		return usecase.PresignedUpload{}, fmt.Errorf("set expires: %w", err)
	}

	if err := policy.SetContentLengthRange(1, params.MaxSizeBytes); err != nil {
		return usecase.PresignedUpload{}, fmt.Errorf("set content length range: %w", err)
	}

	if params.ContentTypePrefix != "" {
		if err := policy.SetContentTypeStartsWith(params.ContentTypePrefix); err != nil {
			return usecase.PresignedUpload{}, fmt.Errorf("set content type prefix: %w", err)
		}
	}

	endpoint, formData, err := s.client.PresignedPostPolicy(ctx, policy)
	if err != nil {
		return usecase.PresignedUpload{}, fmt.Errorf("presign post policy: %w", err)
	}

	public, err := url.JoinPath(s.publicBase, endpoint.Path)
	if err != nil {
		return usecase.PresignedUpload{}, fmt.Errorf("join public base: %w", err)
	}

	return usecase.PresignedUpload{URL: public, Fields: formData}, nil
}

func (s *Store) Ping(ctx context.Context) error {
	if s.guard.InTransaction(ctx) {
		return fmt.Errorf("%w: ping object", ErrInTransaction)
	}

	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("check bucket %s: %w", s.bucket, err)
	}

	if !exists {
		return fmt.Errorf("bucket %s does not exist", s.bucket)
	}

	return nil
}

func (s *Store) Head(
	ctx context.Context,
	location domain.Location,
) (usecase.ObjectInfo, error) {
	if s.guard.InTransaction(ctx) {
		return usecase.ObjectInfo{}, fmt.Errorf("%w: head object", ErrInTransaction)
	}

	info, err := s.client.StatObject(ctx, s.bucket, location.String(), minio.StatObjectOptions{})
	if err != nil {
		if minio.ToErrorResponse(err).StatusCode == http.StatusNotFound {
			return usecase.ObjectInfo{}, fmt.Errorf(
				"%w: %s", usecase.ErrObjectNotFound, location.String(),
			)
		}

		return usecase.ObjectInfo{}, fmt.Errorf("stat %q: %w", location.String(), err)
	}

	return usecase.ObjectInfo{SizeBytes: info.Size, ContentType: info.ContentType}, nil
}

func (s *Store) Get(ctx context.Context, location domain.Location) (io.ReadCloser, error) {
	if s.guard.InTransaction(ctx) {
		return nil, fmt.Errorf("%w: get object", ErrInTransaction)
	}

	object, err := s.client.GetObject(ctx, s.bucket, location.String(), minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("get %q: %w", location.String(), err)
	}

	if _, err := object.Stat(); err != nil {
		_ = object.Close()

		if minio.ToErrorResponse(err).StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("%w: %s", usecase.ErrObjectNotFound, location.String())
		}

		return nil, fmt.Errorf("stat %q: %w", location.String(), err)
	}

	return object, nil
}

func (s *Store) Put(ctx context.Context, params usecase.PutObjectParams) error {
	if s.guard.InTransaction(ctx) {
		return fmt.Errorf("%w: put object", ErrInTransaction)
	}

	_, err := s.client.PutObject(
		ctx,
		s.bucket,
		params.Location.String(),
		params.Body,
		params.Size.Bytes(),
		minio.PutObjectOptions{ContentType: params.MimeType.String()},
	)
	if err != nil {
		return fmt.Errorf("put %q: %w", params.Location.String(), err)
	}

	return nil
}

func (s *Store) Copy(ctx context.Context, from, to domain.Location) error {
	if s.guard.InTransaction(ctx) {
		return fmt.Errorf("%w: copy object", ErrInTransaction)
	}

	_, err := s.client.CopyObject(
		ctx,
		minio.CopyDestOptions{Bucket: s.bucket, Object: to.String()},
		minio.CopySrcOptions{Bucket: s.bucket, Object: from.String()},
	)
	if err != nil {
		return fmt.Errorf("copy %q to %q: %w", from.String(), to.String(), err)
	}

	return nil
}

func (s *Store) Delete(ctx context.Context, locations ...domain.Location) error {
	if s.guard.InTransaction(ctx) {
		return fmt.Errorf("%w: delete object", ErrInTransaction)
	}

	if len(locations) == 0 {
		return nil
	}

	objects := make(chan minio.ObjectInfo, len(locations))

	for _, location := range locations {
		objects <- minio.ObjectInfo{Key: location.String()}
	}

	close(objects)

	var firstErr error

	for removal := range s.client.RemoveObjects(
		ctx, s.bucket, objects, minio.RemoveObjectsOptions{},
	) {
		if removal.Err != nil && firstErr == nil {
			firstErr = fmt.Errorf("remove %q: %w", removal.ObjectName, removal.Err)
		}
	}

	return firstErr
}
