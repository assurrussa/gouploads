package clientresizer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"

	logger "github.com/assurrussa/gologger"
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
}

type artifactHTTPClient interface {
	Do(request *http.Request) (*http.Response, error)
}

//go:generate options-gen -out-filename=service_options.gen.go -from-struct=Options
type Options struct {
	client               httpClient `option:"mandatory" validate:"required"`
	mediaImageResizerURL string     `option:"mandatory" validate:"required,url"`
	mediaVideoResizerURL string     `option:"mandatory" validate:"required,url"`
	imageResizerToken    string
	videoResizerToken    string
	tokenProvider        func(context.Context) (imageToken string, videoToken string)
	artifactClient       artifactHTTPClient
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
	if opts.artifactClient == nil {
		opts.artifactClient = &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
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
	if err := req.Validate(); err != nil {
		return ResponseDownload{}, fmt.Errorf("validate artifact request: %w", err)
	}

	artifactURL, err := validateArtifactURL(req.URL, s.mediaResizerURL(req.TypeMedia))
	if err != nil {
		return ResponseDownload{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, artifactURL.String(), nil)
	if err != nil {
		return ResponseDownload{}, fmt.Errorf("create artifact request: %w", err)
	}

	resp, err := s.artifactClient.Do(request)
	if err != nil {
		return ResponseDownload{}, fmt.Errorf(
			"download artifact %s: %w",
			safeURL(artifactURL),
			redactTransportURL(err),
		)
	}
	if resp == nil || resp.Body == nil {
		return ResponseDownload{}, fmt.Errorf("download artifact %s: empty response", safeURL(artifactURL))
	}
	if resp.Request != nil && resp.Request.URL != nil {
		if _, finalURLErr := validateArtifactURL(resp.Request.URL.String(), s.mediaResizerURL(req.TypeMedia)); finalURLErr != nil {
			_ = resp.Body.Close()
			return ResponseDownload{}, fmt.Errorf("download artifact redirected outside allowed origin: %w", finalURLErr)
		}
	}

	if resp.StatusCode != http.StatusOK {
		defer func() { _ = resp.Body.Close() }()
		return ResponseDownload{}, fmt.Errorf(
			"download artifact %s: unexpected status %d",
			safeURL(artifactURL), resp.StatusCode,
		)
	}

	s.logger.DebugContext(ctx,
		"opened artifact stream for preset",
		slog.String("preset", req.Preset),
		slog.String("url", safeURL(artifactURL)),
		slog.Int64("content_length", resp.ContentLength),
	)

	return ResponseDownload{
		Body:          resp.Body,
		ContentType:   resp.Header.Get(fiber.HeaderContentType),
		ContentLength: resp.ContentLength,
	}, nil
}

func validateArtifactURL(rawArtifactURL, rawResizerURL string) (*url.URL, error) {
	artifactURL, err := url.Parse(strings.TrimSpace(rawArtifactURL))
	if err != nil {
		return nil, errors.New("parse artifact URL")
	}
	resizerURL, err := url.Parse(strings.TrimSpace(rawResizerURL))
	if err != nil {
		return nil, fmt.Errorf("parse media-resizer URL: %w", err)
	}
	if artifactURL.User != nil || artifactURL.Scheme == "" || artifactURL.Host == "" {
		return nil, errors.New("artifact URL requires an absolute origin without userinfo")
	}
	if !strings.EqualFold(artifactURL.Scheme, resizerURL.Scheme) || !strings.EqualFold(artifactURL.Host, resizerURL.Host) {
		return nil, fmt.Errorf(
			"artifact URL origin %s does not match media-resizer origin %s",
			safeOrigin(artifactURL), safeOrigin(resizerURL),
		)
	}
	if strings.EqualFold(artifactURL.Scheme, "http") && !isInternalHost(artifactURL.Hostname()) {
		return nil, fmt.Errorf("external artifact origin %s requires HTTPS", safeOrigin(artifactURL))
	}
	if !strings.EqualFold(artifactURL.Scheme, "http") && !strings.EqualFold(artifactURL.Scheme, "https") {
		return nil, errors.New("artifact URL scheme must be HTTP or HTTPS")
	}

	return artifactURL, nil
}

func isInternalHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".internal") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate()
	}

	return host != "" && !strings.Contains(host, ".")
}

func safeURL(value *url.URL) string {
	if value == nil {
		return ""
	}

	return safeOrigin(value) + value.EscapedPath()
}

func safeOrigin(value *url.URL) string {
	if value == nil {
		return ""
	}

	return strings.ToLower(value.Scheme) + "://" + value.Host
}

func redactTransportURL(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		return urlErr.Err
	}

	return err
}

func (s *Service) getMediaResizerURL(req Request) string {
	return s.mediaResizerURL(req.TypeMedia)
}

func (s *Service) mediaResizerURL(mediaType string) string {
	switch mediaType {
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
