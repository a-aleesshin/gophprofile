package rest

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
	"github.com/a-aleesshin/gophprofile/profile/internal/usecase"
)

func (h *Handler) listAvatars(w http.ResponseWriter, r *http.Request) {
	user, err := domain.NewUserID(r.PathValue("user_id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "profile_not_found")

		return
	}

	limit := 0

	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 0 {
			writeError(w, http.StatusBadRequest, "invalid_request")

			return
		}
	}

	ctx, cancel := h.withTimeout(r)
	defer cancel()

	viewer, _ := UserFrom(r.Context())

	result, err := h.handlers.ListAvatars.Handle(ctx, usecase.ListAvatarsCommand{
		UserID: user,
		Viewer: viewer,
		Limit:  limit,
		Cursor: r.URL.Query().Get("cursor"),
	})
	if err != nil {
		writeFailure(w, err)

		return
	}

	body := listBody{
		Items:      make([]avatarBody, 0, len(result.Items)),
		NextCursor: result.NextCursor,
	}

	for _, view := range result.Items {
		body.Items = append(body.Items,
			toAvatarBody(view, result.URLs[view.ID.String()], result.Owner))
	}

	writeJSON(w, http.StatusOK, body)
}

func (h *Handler) resolveCurrentAvatar(w http.ResponseWriter, r *http.Request) {
	user, err := domain.NewUserID(r.PathValue("user_id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "profile_not_found")

		return
	}

	ctx, cancel := h.withTimeout(r)
	defer cancel()

	result, err := h.handlers.ResolveCurrentAvatar.Handle(ctx,
		usecase.ResolveCurrentAvatarCommand{
			UserID: user,
			Size:   r.URL.Query().Get("size"),
		})
	if err != nil {
		writeFailure(w, err)

		return
	}

	redirect(w, result)
}

func (h *Handler) bindEmail(w http.ResponseWriter, r *http.Request) {
	caller, carried := UserFrom(r.Context())
	if !carried {
		writeError(w, http.StatusUnauthorized, "no_user")

		return
	}

	target, err := domain.NewUserID(r.PathValue("user_id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "profile_not_found")

		return
	}

	if !caller.Equal(target) {
		writeError(w, http.StatusForbidden, "forbidden")

		return
	}

	var request bindEmailRequest

	if decodeErr := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).
		Decode(&request); decodeErr != nil {
		writeError(w, http.StatusBadRequest, "invalid_body")

		return
	}

	email, err := domain.NewEmail(request.Email)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{
			Error: "invalid_email", Details: err.Error(),
		})

		return
	}

	ctx, cancel := h.withTimeout(r)
	defer cancel()

	result, err := h.handlers.BindEmail.Handle(ctx, usecase.BindEmailCommand{
		UserID: target,
		Email:  email,
	})
	if err != nil {
		writeFailure(w, err)

		return
	}

	writeJSON(w, http.StatusOK, bindEmailBody{
		UserID:    target.String(),
		EmailHash: result.EmailHash.String(),
	})
}
