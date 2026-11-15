package rest

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/a-aleesshin/gophprofile/profile/internal/adapter/identity"
	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
)

func (h *Handler) openSession(w http.ResponseWriter, r *http.Request) {
	var request openSessionRequest

	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).
		Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body")

		return
	}

	if err := h.identity.Login(request.Secret); err != nil {
		writeError(w, http.StatusUnauthorized, "bad_secret")

		return
	}

	user, err := domain.NewUserID(request.UserID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{
			Error: "invalid_user", Details: err.Error(),
		})

		return
	}

	token, expires, err := h.identity.Issue(user)
	if err != nil {
		if errors.Is(err, identity.ErrBadToken) {
			writeError(w, http.StatusBadRequest, "invalid_user")

			return
		}

		writeFailure(w, err)

		return
	}

	writeJSON(w, http.StatusCreated, openSessionBody{
		UserID:    user.String(),
		Token:     token,
		ExpiresAt: expires,
	})
}
