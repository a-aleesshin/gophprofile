package rest

import (
	"context"
	"net/http"

	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
)

const HeaderUserID = "X-User-ID"

type identityKey struct{}

func WithUser(ctx context.Context, user domain.UserID) context.Context {
	return context.WithValue(ctx, identityKey{}, user)
}

func UserFrom(ctx context.Context) (domain.UserID, bool) {
	user, carried := ctx.Value(identityKey{}).(domain.UserID)

	return user, carried && !user.IsZero()
}

func requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := r.Header.Get(HeaderUserID)
		if raw == "" {
			writeError(w, http.StatusUnauthorized, "no_user")

			return
		}

		user, err := domain.NewUserID(raw)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "bad_user")

			return
		}

		next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), user)))
	})
}
