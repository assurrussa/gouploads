package http

import (
	"github.com/gofiber/fiber/v3"

	"github.com/assurrussa/gouploads/shared/uploadstrategies"
)

type FiberUploadHandlerWrapper struct {
	handler *Handler
}

func NewFiberUploadHandlerWrapper(handler *Handler) *FiberUploadHandlerWrapper {
	return &FiberUploadHandlerWrapper{handler: handler}
}

func (h *FiberUploadHandlerWrapper) RegisterStrategy(contextName string, strategy uploadstrategies.Strategy) {
	h.handler.RegisterStrategy(contextName, strategy)
}

func (h *FiberUploadHandlerWrapper) RegisterGroupRoutes(prefix string, router fiber.Router, middlewares ...fiber.Handler) {
	h.handler.SetPrefix(prefix)

	group := router.Group(prefix)

	for _, m := range middlewares {
		group.Use(m)
	}

	group.Get("", h.handler.ListFiles)
	group.Get("file/:id", h.handler.GetFile)
	group.Get("tasks/:id", h.handler.GetFile)
	group.Get("upload/tasks/:id", h.handler.GetFile)

	// TUS
	group.Options("tus", h.handler.TusOptions)
	group.Post("tus", h.handler.TusCreate)
	group.Head("tus/:id", h.handler.TusHead)
	group.Patch("tus/:id", h.handler.TusPatch)
	group.Post("tus/:id/complete", h.handler.TusComplete)

	// Standard
	group.Post("", h.handler.Upload)
	group.Post("rich-text", h.handler.Upload) // Alias for backward compatibility, logical handling is in strategy!
	group.Delete(":id", h.handler.DeleteFile)
}
