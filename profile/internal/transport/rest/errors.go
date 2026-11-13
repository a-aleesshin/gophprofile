package rest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
	"github.com/a-aleesshin/gophprofile/profile/internal/usecase"
)

type errorBody struct {
	Error   string `json:"error"`
	Details string `json:"details,omitempty"`
	Status  string `json:"status,omitempty"`
}

type failure struct {
	status  int
	reason  string
	details bool
}

var failures = []struct {
	err error
	as  failure
}{
	{usecase.ErrUnsupportedMimeType, failure{http.StatusBadRequest, "invalid_mime_type", true}},
	{usecase.ErrInvalidCursor, failure{http.StatusBadRequest, "invalid_cursor", true}},
	{usecase.ErrInvalidCommand, failure{http.StatusBadRequest, "invalid_request", true}},

	{usecase.ErrForbidden, failure{http.StatusForbidden, "forbidden", false}},

	{domain.ErrAvatarNotFound, failure{http.StatusNotFound, "avatar_not_found", false}},
	{domain.ErrProfileNotFound, failure{http.StatusNotFound, "profile_not_found", false}},
	{usecase.ErrVariantAbsent, failure{http.StatusNotFound, "variant_not_found", false}},
	{usecase.ErrUploadNotFound, failure{http.StatusNotFound, "upload_not_found", false}},

	{usecase.ErrAvatarNotReady, failure{http.StatusConflict, "avatar_not_ready", false}},
	{usecase.ErrTooManyAvatars, failure{http.StatusConflict, "too_many_avatars", true}},
	{usecase.ErrUploadNotUsable, failure{http.StatusConflict, "upload_expired", true}},
	{domain.ErrEmailTaken, failure{http.StatusConflict, "email_taken", false}},
	{domain.ErrProfileExists, failure{http.StatusConflict, "conflict", false}},
	{usecase.ErrStaleVersion, failure{http.StatusConflict, "conflict", false}},

	{usecase.ErrFileTooLarge, failure{http.StatusRequestEntityTooLarge, "file_too_large", true}},

	{usecase.ErrFileServiceUnavailable, failure{http.StatusServiceUnavailable, "file_unavailable", false}},

	{usecase.ErrFileServiceTimeout, failure{http.StatusGatewayTimeout, "timeout", false}},
	{context.DeadlineExceeded, failure{http.StatusGatewayTimeout, "timeout", false}},
}

func writeFailure(w http.ResponseWriter, err error) {
	for _, candidate := range failures {
		if !errors.Is(err, candidate.err) {
			continue
		}

		body := errorBody{Error: candidate.as.reason}

		if candidate.as.details {
			body.Details = err.Error()
		}

		writeJSON(w, candidate.as.status, body)

		return
	}

	writeJSON(w, http.StatusInternalServerError, errorBody{Error: "internal"})
}

func writeError(w http.ResponseWriter, status int, reason string) {
	writeJSON(w, status, errorBody{Error: reason})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(body)
}
