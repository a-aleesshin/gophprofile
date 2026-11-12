package rest

import (
	"encoding/json"
	"net/http"

	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
	"github.com/a-aleesshin/gophprofile/profile/internal/usecase"
)

func (h *Handler) startUpload(w http.ResponseWriter, r *http.Request) {
	user, carried := UserFrom(r.Context())
	if !carried {
		writeError(w, http.StatusUnauthorized, "no_user")

		return
	}

	var request startUploadRequest

	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).
		Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body")

		return
	}

	ctx, cancel := h.withTimeout(r)
	defer cancel()

	result, err := h.handlers.StartUpload.Handle(ctx, usecase.StartUploadCommand{
		UserID:    user,
		FileName:  request.FileName,
		MimeType:  request.MimeType,
		SizeBytes: request.SizeBytes,
	})
	if err != nil {
		writeFailure(w, err)

		return
	}

	writeJSON(w, http.StatusCreated, startUploadBody{
		AvatarID: result.AvatarID.String(),
		Status:   result.Status.String(),
		Upload: uploadTicketBody{
			URL:       result.URL,
			Fields:    result.Fields,
			ExpiresAt: result.ExpiresAt,
		},
	})
}

func (h *Handler) completeUpload(w http.ResponseWriter, r *http.Request) {
	user, carried := UserFrom(r.Context())
	if !carried {
		writeError(w, http.StatusUnauthorized, "no_user")

		return
	}

	avatarID, err := domain.ParseAvatarID(r.PathValue("avatar_id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "avatar_not_found")

		return
	}

	ctx, cancel := h.withTimeout(r)
	defer cancel()

	result, err := h.handlers.CompleteUpload.Handle(ctx, usecase.CompleteUploadCommand{
		AvatarID: avatarID,
		UserID:   user,
	})
	if err != nil {
		writeFailure(w, err)

		return
	}

	writeJSON(w, http.StatusAccepted, completeUploadBody{
		AvatarID: result.AvatarID.String(),
		Status:   result.Status.String(),
	})
}

func (h *Handler) getAvatar(w http.ResponseWriter, r *http.Request) {
	avatarID, err := domain.ParseAvatarID(r.PathValue("avatar_id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "avatar_not_found")

		return
	}

	ctx, cancel := h.withTimeout(r)
	defer cancel()

	result, err := h.handlers.GetAvatar.Handle(ctx, usecase.GetAvatarCommand{
		AvatarID: avatarID,
	})
	if err != nil {
		writeFailure(w, err)

		return
	}

	writeJSON(w, http.StatusOK, toAvatarBody(result.Avatar, result.URLs))
}

func (h *Handler) resolveAvatar(w http.ResponseWriter, r *http.Request) {
	avatarID, err := domain.ParseAvatarID(r.PathValue("avatar_id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "avatar_not_found")

		return
	}

	ctx, cancel := h.withTimeout(r)
	defer cancel()

	result, err := h.handlers.ResolveAvatar.Handle(ctx, usecase.ResolveAvatarCommand{
		AvatarID: avatarID,
		Size:     r.URL.Query().Get("size"),
	})
	if err != nil {
		writeFailure(w, err)

		return
	}

	redirect(w, result)
}

func (h *Handler) resolveByEmail(w http.ResponseWriter, r *http.Request) {
	hash, err := domain.ParseEmailHash(r.PathValue("email_hash"))
	if err != nil {
		writeError(w, http.StatusNotFound, "profile_not_found")

		return
	}

	ctx, cancel := h.withTimeout(r)
	defer cancel()

	result, err := h.handlers.ResolveByEmail.Handle(ctx, usecase.ResolveByEmailCommand{
		EmailHash: hash,
		Size:      r.URL.Query().Get("size"),
	})
	if err != nil {
		writeFailure(w, err)

		return
	}

	redirect(w, result)
}

func (h *Handler) deleteAvatar(w http.ResponseWriter, r *http.Request) {
	user, carried := UserFrom(r.Context())
	if !carried {
		writeError(w, http.StatusUnauthorized, "no_user")

		return
	}

	avatarID, err := domain.ParseAvatarID(r.PathValue("avatar_id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "avatar_not_found")

		return
	}

	ctx, cancel := h.withTimeout(r)
	defer cancel()

	if _, err := h.handlers.DeleteAvatar.Handle(ctx, usecase.DeleteAvatarCommand{
		AvatarID: avatarID,
		UserID:   user,
	}); err != nil {
		writeFailure(w, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}
