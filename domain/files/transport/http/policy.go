package http

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/assurrussa/gouploads/domain/files/model"
	"github.com/assurrussa/gouploads/domain/files/service/uploadservice"
	fileshared "github.com/assurrussa/gouploads/domain/files/shared"
	"github.com/assurrussa/gouploads/shared/uploadstrategies"
)

// ResourceAuthorizer checks access to the actual object, not merely whether the
// actor has authenticated. action is read, list, upload, resume, or delete.
// For list/upload/resume, File contains the requested ObjectType and ObjectID.
type ResourceAuthorizer func(context.Context, uploadstrategies.UploadContext, string, model.File) error

// ActorResolver makes the distinction between an application user and manager
// explicit. Its return values are managerID and userID, respectively.
type ActorResolver func(context.Context, uploadstrategies.UploadContext) (int64, int64, error)

type HandlerPolicy struct {
	// AllowDefaultStrategy permits the empty context only. An unknown nonempty
	// context is always rejected, so a spelling error cannot bypass CanUpload.
	AllowDefaultStrategy bool
	// AllowAnonymousTUS is a bearer-capability policy for unowned sessions. It is
	// disabled by default. Generic completion still requires an upload actor.
	AllowAnonymousTUS bool
	Authorize         ResourceAuthorizer
	// TrustRouteGuards is an explicit declaration that the embedding application
	// already performs object-level authorization on every mounted operation.
	TrustRouteGuards bool
	ResolveActor     ActorResolver
}

// requestError carries an HTTP status without writing an HTTP response. Only a
// top-level handler renders it; successful JSON serialization is not a failure.
type requestError struct {
	status  int
	message string
}

func (e requestError) Error() string          { return e.message }
func reject(status int, message string) error { return requestError{status, message} }

func (h *Handler) writeRequestError(c fiber.Ctx, err error) error {
	var requestErr requestError
	if errors.As(err, &requestErr) {
		return h.jsonError(c, requestErr.status, requestErr.message)
	}
	var clientErr uploadservice.ClientError
	if errors.As(err, &clientErr) {
		return h.jsonError(c, http.StatusBadRequest, clientErr.Message)
	}
	var validationErr uploadservice.ValidationError
	if errors.As(err, &validationErr) {
		return h.jsonValidationError(c, validationErr.Errors)
	}
	return h.jsonError(c, http.StatusInternalServerError, "failed to process upload")
}

func (h *Handler) authorize(ctx context.Context, actor uploadstrategies.UploadContext, action string, file model.File) error {
	if h.policy.Authorize != nil {
		if err := h.policy.Authorize(ctx, actor, action, file); err != nil {
			return reject(http.StatusForbidden, "access to upload object is denied")
		}
		return nil
	}
	if !h.policy.TrustRouteGuards {
		return reject(http.StatusForbidden, "object authorization is not configured")
	}
	return nil
}

func (h *Handler) actorIDs(ctx context.Context, actor uploadstrategies.UploadContext) (managerID, userID int64, err error) {
	if h.policy.ResolveActor != nil {
		return h.policy.ResolveActor(ctx, actor)
	}
	// Preserve the legacy administrative identity mapping. Non-admin hosts use
	// the explicit resolver instead of silently changing existing attachments.
	if actor.UserID <= 0 || actor.UserUUID.IsZero() {
		return 0, 0, reject(http.StatusUnauthorized, "upload actor is required")
	}
	return actor.UserID, 0, nil
}

func requestObject(objectType string, objectID int64) model.File {
	file := model.File{}
	file.ObjectType = fileshared.FileObjectType(objectType)
	id := fileshared.FileObjectID(objectID)
	file.ObjectID = &id
	return file
}

func normalizedContext(value string) string { return strings.ToLower(strings.TrimSpace(value)) }
