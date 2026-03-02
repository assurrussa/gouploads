package clientresizer

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/assurrussa/goshared/pkg/logger"
	transporthttp "github.com/assurrussa/goshared/pkg/transport/http"
	"github.com/gofiber/fiber/v3"
)

const (
	mediaTypeImage = "image"
	mediaTypeVideo = "video"
)

//go:generate toolsmocks

type httpClient interface {
	DoWithRequestAndParse(ctx context.Context, request transporthttp.Request, data any) error
	DoWithRequest(ctx context.Context, request transporthttp.Request) (*http.Response, error)
}

//go:generate options-gen -out-filename=service_options.gen.go -from-struct=Options
type Options struct {
	client               httpClient `option:"mandatory" validate:"required"`
	mediaImageResizerURL string     `option:"mandatory" validate:"required,url"`
	mediaVideoResizerURL string     `option:"mandatory" validate:"required,url"`
	imageResizerToken    string
	videoResizerToken    string
	tokenProvider        func(context.Context) (imageToken string, videoToken string)
	logger               logger.Logger `option:"mandatory" validate:"required"`
}

type Service struct {
	Options
}

func Must(opts Options) *Service {
	service, err := New(opts)
	if err != nil {
		panic(err)
	}
	return service
}

func New(opts Options) (*Service, error) {
	if err := opts.Validate(); err != nil {
		return nil, fmt.Errorf("validate options: %w", err)
	}

	return &Service{Options: opts}, nil
}

func (s *Service) SendResize(ctx context.Context, req Request) (Response, error) {
	rawToken := s.tokenForType(ctx, req.TypeMedia)

	request := transporthttp.Request{
		ExpectStatusCode: http.StatusAccepted,
		Method:           http.MethodPost,
		URL:              s.getMediaResizerURL(req),
		Body:             bytes.NewReader(req.Data),
		Headers: map[string]string{
			fiber.HeaderContentType: fiber.MIMEApplicationJSONCharsetUTF8,
		},
	}
	if rawToken != "" {
		request.Headers[fiber.HeaderAuthorization] = "Bearer " + rawToken
		request.Headers["X-API-Token"] = rawToken
	}

	var resp Response
	err := s.client.DoWithRequestAndParse(ctx, request, &resp)
	if err != nil {
		return Response{}, fmt.Errorf("DoWithRequestAndParse : %w", err)
	}

	return resp, nil
}

func (s *Service) DownloadFile(ctx context.Context, req RequestDownload) (ResponseDownload, error) {
	rawToken := s.tokenForType(ctx, req.TypeMedia)

	request := transporthttp.Request{
		Method: http.MethodGet,
		URL:    req.URL,
	}
	if rawToken != "" {
		if request.Headers == nil {
			request.Headers = make(map[string]string)
		}
		request.Headers[fiber.HeaderAuthorization] = "Bearer " + rawToken
		request.Headers["X-API-Token"] = rawToken
	}

	resp, err := s.client.DoWithRequest(ctx, request)
	if err != nil {
		return ResponseDownload{}, fmt.Errorf("DoWithRequestAndParse : %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return ResponseDownload{}, fmt.Errorf("ReadAll body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return ResponseDownload{}, fmt.Errorf("unexpected status code: %d %s", resp.StatusCode, string(data))
	}

	s.logger.DebugContext(ctx,
		"downloaded artifact for preset",
		slog.String("preset", req.Preset),
		slog.String("url", req.URL),
		slog.Int("bytes", len(data)),
		slog.Float64("KB", float64(len(data))/1024),
		slog.Float64("MB", float64(len(data))/(1024*1024)),
	)

	return ResponseDownload{
		Body: data,
	}, nil
}

func (s *Service) getMediaResizerURL(req Request) string {
	switch req.TypeMedia {
	case "video":
		return s.mediaVideoResizerURL
	default:
		return s.mediaImageResizerURL
	}
}

func (s *Service) tokenForType(ctx context.Context, mediaType string) string {
	if s.tokenProvider != nil {
		imgToken, vidToken := s.tokenProvider(ctx)
		switch mediaType {
		case mediaTypeVideo:
			return vidToken
		default:
			return imgToken
		}
	}

	switch mediaType {
	case mediaTypeVideo:
		if s.videoResizerToken != "" {
			return s.videoResizerToken
		}
	case mediaTypeImage:
		if s.imageResizerToken != "" {
			return s.imageResizerToken
		}
	default:
		if s.imageResizerToken != "" {
			return s.imageResizerToken
		}
	}
	return ""
}
