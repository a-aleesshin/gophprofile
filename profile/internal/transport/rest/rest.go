package rest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/a-aleesshin/gophprofile/internal/httpserver"
	"github.com/a-aleesshin/gophprofile/profile/internal/usecase"
)

var ErrIncompleteHandler = errors.New("incomplete rest handler")

const (
	maxBodyBytes = 4 << 10

	redirectCache    = "public, max-age=300"
	placeholderCache = "public, max-age=30"
)

type uploadStarter interface {
	Handle(
		ctx context.Context,
		cmd usecase.StartUploadCommand,
	) (usecase.StartUploadResult, error)
}

type uploadCompleter interface {
	Handle(
		ctx context.Context,
		cmd usecase.CompleteUploadCommand,
	) (usecase.CompleteUploadResult, error)
}

type avatarReader interface {
	Handle(ctx context.Context, cmd usecase.GetAvatarCommand) (usecase.GetAvatarResult, error)
}

type avatarLister interface {
	Handle(
		ctx context.Context,
		cmd usecase.ListAvatarsCommand,
	) (usecase.ListAvatarsResult, error)
}

type avatarRedirector interface {
	Handle(ctx context.Context, cmd usecase.ResolveAvatarCommand) (usecase.ResolveResult, error)
}

type currentAvatarRedirector interface {
	Handle(
		ctx context.Context,
		cmd usecase.ResolveCurrentAvatarCommand,
	) (usecase.ResolveResult, error)
}

type emailRedirector interface {
	Handle(
		ctx context.Context,
		cmd usecase.ResolveByEmailCommand,
	) (usecase.ResolveResult, error)
}

type avatarRemover interface {
	Handle(
		ctx context.Context,
		cmd usecase.DeleteAvatarCommand,
	) (usecase.DeleteAvatarResult, error)
}

type emailBinder interface {
	Handle(ctx context.Context, cmd usecase.BindEmailCommand) (usecase.BindEmailResult, error)
}

type Config struct {
	Timeout time.Duration `env:"TIMEOUT" envDefault:"5s"`
}

type Handlers struct {
	StartUpload          uploadStarter
	CompleteUpload       uploadCompleter
	GetAvatar            avatarReader
	ListAvatars          avatarLister
	ResolveAvatar        avatarRedirector
	ResolveCurrentAvatar currentAvatarRedirector
	ResolveByEmail       emailRedirector
	DeleteAvatar         avatarRemover
	BindEmail            emailBinder
}

type Handler struct {
	handlers Handlers
	logger   *slog.Logger
	timeout  time.Duration
}

func NewHandler(handlers Handlers, cfg Config, logger *slog.Logger) (*Handler, error) {
	missing := map[string]bool{
		"StartUpload":          handlers.StartUpload == nil,
		"CompleteUpload":       handlers.CompleteUpload == nil,
		"GetAvatar":            handlers.GetAvatar == nil,
		"ListAvatars":          handlers.ListAvatars == nil,
		"ResolveAvatar":        handlers.ResolveAvatar == nil,
		"ResolveCurrentAvatar": handlers.ResolveCurrentAvatar == nil,
		"ResolveByEmail":       handlers.ResolveByEmail == nil,
		"DeleteAvatar":         handlers.DeleteAvatar == nil,
		"BindEmail":            handlers.BindEmail == nil,
	}

	for name, absent := range missing {
		if absent {
			return nil, fmt.Errorf("%w: no handler for %s", ErrIncompleteHandler, name)
		}
	}

	switch {
	case cfg.Timeout <= 0:
		return nil, fmt.Errorf("%w: timeout must be positive", ErrIncompleteHandler)
	case logger == nil:
		return nil, fmt.Errorf("%w: logger is required", ErrIncompleteHandler)
	}

	return &Handler{handlers: handlers, logger: logger, timeout: cfg.Timeout}, nil
}

func (h *Handler) Register(mux *http.ServeMux) {
	owned := map[string]http.HandlerFunc{
		"POST /api/v1/avatars/uploads":              h.startUpload,
		"POST /api/v1/avatars/{avatar_id}/complete": h.completeUpload,
		"DELETE /api/v1/avatars/{avatar_id}":        h.deleteAvatar,
		"PUT /api/v1/users/{user_id}":               h.bindEmail,
	}

	for pattern, handler := range owned {
		mux.Handle(pattern, h.wrap(requireUser(handler)))
	}

	public := map[string]http.HandlerFunc{
		"GET /api/v1/avatars/{avatar_id}":          h.resolveAvatar,
		"GET /api/v1/avatars/{avatar_id}/metadata": h.getAvatar,
		"GET /api/v1/emails/{email_hash}/avatar":   h.resolveByEmail,
		"GET /api/v1/users/{user_id}/avatars":      h.listAvatars,
		"GET /api/v1/users/{user_id}/avatar":       h.resolveCurrentAvatar,
	}

	for pattern, handler := range public {
		mux.Handle(pattern, h.wrap(handler))
	}
}

// Recovery is not here: httpserver.New wraps every server it builds, so a panic
// cannot escape whichever handler it happens in. Logging is per route, because
// the route pattern exists only after the mux has matched.
func (h *Handler) wrap(next http.Handler) http.Handler {
	return httpserver.Log(h.logger, next)
}

func (h *Handler) withTimeout(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), h.timeout)
}

func redirect(w http.ResponseWriter, result usecase.ResolveResult) {
	cache := redirectCache
	if result.Placeholder {
		cache = placeholderCache
	}

	w.Header().Set("Cache-Control", cache)
	w.Header().Set("Location", result.URL)
	w.WriteHeader(http.StatusFound)
}
