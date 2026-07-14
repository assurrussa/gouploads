package http

import (
	"github.com/gofiber/fiber/v3"

	"github.com/assurrussa/gouploads/shared/uploadstrategies"
)

type FiberUploadHandlerWrapper struct {
	handler *Handler
}

type RouteGuards struct {
	Common []fiber.Handler
	Read   []fiber.Handler
	Create []fiber.Handler
	Delete []fiber.Handler
}

func NewFiberUploadHandlerWrapper(handler *Handler) *FiberUploadHandlerWrapper {
	return &FiberUploadHandlerWrapper{handler: handler}
}

func (h *FiberUploadHandlerWrapper) RegisterStrategy(contextName string, strategy uploadstrategies.Strategy) {
	h.handler.RegisterStrategy(contextName, strategy)
}

func (h *FiberUploadHandlerWrapper) RegisterGroupRoutes(prefix string, router fiber.Router, middlewares ...fiber.Handler) {
	h.RegisterGroupRoutesWithGuards(prefix, router, RouteGuards{Common: middlewares})
}

func (h *FiberUploadHandlerWrapper) RegisterGroupRoutesWithGuards(
	prefix string,
	router fiber.Router,
	guards RouteGuards,
) {
	group := router.Group(prefix)

	for _, m := range guards.Common {
		group.Use(m)
	}

	registerRoute(group.Get, "", guards.Read, h.handler.ListFiles)
	registerRoute(group.Get, "file/:id", guards.Read, h.handler.GetFile)
	registerRoute(group.Get, "tasks/:id", guards.Read, h.handler.GetFile)
	registerRoute(group.Get, "upload/tasks/:id", guards.Read, h.handler.GetFile)

	// TUS
	registerRoute(group.Options, "tus", guards.Create, h.handler.TusOptions)
	registerRoute(group.Post, "tus", guards.Create, h.handler.TusCreate)
	registerRoute(group.Head, "tus/:id", guards.Create, h.handler.TusHead)
	registerRoute(group.Patch, "tus/:id", guards.Create, h.handler.TusPatch)
	registerRoute(group.Post, "tus/:id/complete", guards.Create, h.handler.TusComplete)

	// Standard
	registerRoute(group.Post, "", guards.Create, h.handler.Upload)
	registerRoute(group.Post, "rich-text", guards.Create, h.handler.Upload) // Alias for backward compatibility.
	registerRoute(group.Delete, ":id", guards.Delete, h.handler.DeleteFile)
}

// RegisterCMSTusRoutes mounts only the resumable create/resume transport.
// Finalization and deletion intentionally remain owned by the CMS service.
func (h *FiberUploadHandlerWrapper) RegisterCMSTusRoutes(
	prefix string,
	router fiber.Router,
	middlewares ...fiber.Handler,
) {
	group := router.Group(prefix)
	for _, middleware := range middlewares {
		group.Use(middleware)
	}

	group.Options("tus", h.handler.TusOptions)
	group.Post("tus", h.handler.TusCreateCMS)
	group.Head("tus/:id", h.handler.TusHead)
	group.Patch("tus/:id", h.handler.TusPatchCMS)
}

func registerRoute(
	register func(string, any, ...any) fiber.Router,
	path string,
	middlewares []fiber.Handler,
	handler fiber.Handler,
) {
	handlers := make([]any, 0, len(middlewares)+1)
	for _, middleware := range middlewares {
		handlers = append(handlers, middleware)
	}
	handlers = append(handlers, handler)
	register(path, handlers[0], handlers[1:]...)
}
