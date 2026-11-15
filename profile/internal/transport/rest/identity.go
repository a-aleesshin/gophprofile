package rest

import (
	"context"
	"net/http"
	"strings"

	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
)

const bearerPrefix = "Bearer "

type identityKey struct{}

func WithUser(ctx context.Context, user domain.UserID) context.Context {
	return context.WithValue(ctx, identityKey{}, user)
}

func UserFrom(ctx context.Context) (domain.UserID, bool) {
	user, carried := ctx.Value(identityKey{}).(domain.UserID)

	return user, carried && !user.IsZero()
}

func bearerOf(r *http.Request) (string, bool) {
	raw := r.Header.Get("Authorization")
	if raw == "" {
		return "", false
	}

	token, found := strings.CutPrefix(raw, bearerPrefix)
	if !found || token == "" {
		return "", false
	}

	return token, true
}

func (h *Handler) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, carried := bearerOf(r)
		if !carried {
			writeError(w, http.StatusUnauthorized, "no_token")

			return
		}

		user, err := h.identity.Verify(token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "bad_token")

			return
		}

		next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), user)))
	})
}

func (h *Handler) optionalUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, carried := bearerOf(r)
		if !carried {
			next.ServeHTTP(w, r)

			return
		}

		user, err := h.identity.Verify(token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "bad_token")

			return
		}

		next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), user)))
	})
}
