package serving

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/a-aleesshin/gophprofile/file/internal/adapter/link"
	"github.com/a-aleesshin/gophprofile/file/internal/domain"
	"github.com/a-aleesshin/gophprofile/file/internal/usecase"
	"github.com/a-aleesshin/gophprofile/internal/httpserver"
)

var ErrInvalidHandler = errors.New("invalid serving handler")

const (
	routePattern = "GET /upload/{file_id}/{object}"

	headerNoSniff = "X-Content-Type-Options"
	headerETag    = "ETag"
)

type Config struct {
	Timeout time.Duration `env:"TIMEOUT" envDefault:"3s"`
}

type variantServer interface {
	Handle(
		ctx context.Context,
		cmd usecase.ServeVariantCommand,
	) (usecase.ServeVariantResult, error)
}

type signatureVerifier interface {
	Verify(params link.VerifyParams) error
}

type Handler struct {
	variants variantServer
	links    signatureVerifier
	logger   *slog.Logger
	timeout  time.Duration
}

func NewHandler(
	variants variantServer,
	links signatureVerifier,
	cfg Config,
	logger *slog.Logger,
) (*Handler, error) {
	switch {
	case variants == nil:
		return nil, fmt.Errorf("%w: variant server is required", ErrInvalidHandler)
	case links == nil:
		return nil, fmt.Errorf("%w: signature verifier is required", ErrInvalidHandler)
	case cfg.Timeout <= 0:
		return nil, fmt.Errorf("%w: timeout must be positive", ErrInvalidHandler)
	case logger == nil:
		return nil, fmt.Errorf("%w: logger is required", ErrInvalidHandler)
	}

	return &Handler{
		variants: variants,
		links:    links,
		logger:   logger,
		timeout:  cfg.Timeout,
	}, nil
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.Handle(routePattern, httpserver.Log(h.logger, http.HandlerFunc(h.serve)))
}

func (h *Handler) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set(headerNoSniff, "nosniff")

	rawID := r.PathValue("file_id")
	object := r.PathValue("object")

	rawVariant, rawExt, split := strings.Cut(object, ".")
	if !split {
		writeError(w, http.StatusNotFound, "not_found")

		return
	}

	fileID, err := domain.ParseFileID(rawID)
	if err != nil || fileID.String() != rawID {
		writeError(w, http.StatusNotFound, "not_found")

		return
	}

	variant, err := domain.NewVariantName(rawVariant)
	if err != nil || variant.String() != rawVariant {
		writeError(w, http.StatusNotFound, "not_found")

		return
	}

	extension, err := domain.NewExtension(rawExt)
	if err != nil || extension.String() != rawExt {
		writeError(w, http.StatusNotFound, "not_found")

		return
	}

	cmd := usecase.ServeVariantCommand{
		FileID:      fileID,
		Variant:     variant,
		Ext:         extension,
		IfNoneMatch: r.Header.Get("If-None-Match"),
	}

	query := r.URL.Query()

	if signature := query.Get("sig"); signature != "" {
		verifyErr := h.links.Verify(link.VerifyParams{
			FileID:  rawID,
			Variant: rawVariant,
			Ext:     rawExt,
			Expires: query.Get("exp"),
			Sig:     signature,
		})

		switch {
		case errors.Is(verifyErr, link.ErrExpired):
			writeError(w, http.StatusGone, "link_expired")

			return
		case verifyErr != nil:
			writeError(w, http.StatusForbidden, "bad_signature")

			return
		}

		cmd.Signed = true
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	lookup := time.AfterFunc(h.timeout, cancel)

	result, err := h.variants.Handle(ctx, cmd)

	if !lookup.Stop() {
		if result.Body != nil {
			_ = result.Body.Close()
		}

		writeError(w, http.StatusGatewayTimeout, "timeout")

		return
	}

	if err != nil {
		writeFailure(w, err)

		return
	}

	header := w.Header()
	header.Set("Cache-Control", result.CacheControl)
	header.Set(headerETag, `"`+result.ETag+`"`)
	header.Set("Last-Modified", result.LastModified.UTC().Format(http.TimeFormat))

	if result.AcceptRanges {
		header.Set("Accept-Ranges", "bytes")
	} else {
		header.Set("Accept-Ranges", "none")
	}

	if result.Disposition != "" {
		header.Set("Content-Disposition", result.Disposition)
	}

	if result.NotModified {
		w.WriteHeader(http.StatusNotModified)

		return
	}

	defer func() { _ = result.Body.Close() }()

	header.Set("Content-Type", result.MimeType)
	header.Set("Content-Length", strconv.FormatInt(result.SizeBytes, 10))

	w.WriteHeader(http.StatusOK)

	_, _ = io.Copy(w, result.Body)
}

func writeFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrVariantAbsent),
		errors.Is(err, domain.ErrFileNotFound),
		errors.Is(err, usecase.ErrUnknownNamespace),
		errors.Is(err, usecase.ErrInvalidCommand):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, usecase.ErrSignatureRequired):
		writeError(w, http.StatusForbidden, "bad_signature")
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, "timeout")
	case errors.Is(err, usecase.ErrStorageUnavailable):
		writeError(w, http.StatusBadGateway, "storage_unavailable")
	default:
		writeError(w, http.StatusInternalServerError, "internal")
	}
}

func writeError(w http.ResponseWriter, status int, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(map[string]string{"error": reason})
}
