package host

import (
	"context"

	logger "github.com/assurrussa/gologger"

	uploadhttp "github.com/assurrussa/gouploads/domain/files/transport/http"
	"github.com/assurrussa/gouploads/shared/uploadstrategies"
)

// UploadResourceAuthorizer must check the actor's permissions for the actual
// object. action is read, list, upload, resume, or delete.
type UploadResourceAuthorizer func(context.Context, UploadContext, string, File) error

// UploadActorResolver returns managerID and userID, in that order.
type UploadActorResolver func(context.Context, UploadContext) (int64, int64, error)

type UploadHandlerPolicy struct {
	AllowDefaultStrategy bool
	AllowAnonymousTUS    bool
	Authorize            UploadResourceAuthorizer
	TrustRouteGuards     bool
	ResolveActor         UploadActorResolver
}

// NewUploadHandlerWithPolicy is the explicit security policy constructor. Its
// zero policy denies requests without object authorization or a declared
// route-guard policy. An unknown named context never falls back to defaults.
func NewUploadHandlerWithPolicy(
	uploader TaskUploader,
	repo FileRepository,
	store TusStore,
	lg logger.Logger,
	builder ContextBuilder,
	composer URLComposer,
	policy UploadHandlerPolicy,
) *UploadHandler {
	internal := uploadhttp.HandlerPolicy{
		AllowDefaultStrategy: policy.AllowDefaultStrategy,
		AllowAnonymousTUS:    policy.AllowAnonymousTUS,
		TrustRouteGuards:     policy.TrustRouteGuards,
	}
	if policy.Authorize != nil {
		internal.Authorize = func(ctx context.Context, actor uploadstrategies.UploadContext, action string, file File) error {
			return policy.Authorize(ctx, toHostUploadContext(actor), action, file)
		}
	}
	if policy.ResolveActor != nil {
		internal.ResolveActor = func(ctx context.Context, actor uploadstrategies.UploadContext) (int64, int64, error) {
			return policy.ResolveActor(ctx, toHostUploadContext(actor))
		}
	}
	return &UploadHandler{inner: uploadhttp.NewHandlerWithPolicy(uploader,
		repo,
		store,
		lg,
		internalContextBuilder(builder),
		uploadhttp.URLComposer(composer),
		internal)}
}
