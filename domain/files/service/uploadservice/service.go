package uploadservice

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	logger "github.com/assurrussa/gologger"
	"github.com/google/uuid"

	uploadconfig "github.com/assurrussa/gouploads/config"
	"github.com/assurrussa/gouploads/domain/files/model"
	deletedfilejob "github.com/assurrussa/gouploads/domain/files/outbox/deleted_file"
	finalizeoriginal "github.com/assurrussa/gouploads/domain/files/outbox/finalize_original"
	sendresizefilejob "github.com/assurrussa/gouploads/domain/files/outbox/send_resize_file"
	"github.com/assurrussa/gouploads/domain/files/shared"
	"github.com/assurrussa/gouploads/internal/filesanitize"
	"github.com/assurrussa/gouploads/internal/pointer"
)

//go:generate options-gen -out-filename=service_options.gen.go -from-struct=Options -defaults-from=func
type Options struct {
	txManager     transactor     `option:"mandatory" validate:"required"`
	outbox        outboxPutter   `option:"mandatory" validate:"required"`
	fileRepo      fileRepository `option:"mandatory" validate:"required"`
	logger        logger.Logger  `option:"mandatory" validate:"required"`
	storage       fileStorage    `option:"mandatory" validate:"required"`
	dirPrefix     shared.FolderPrefixPath
	dirTempPrefix shared.FolderPrefixPath
	validators    []UploadValidator
}

func getDefaultOptions() Options {
	return Options{
		dirPrefix:     shared.FolderPrefixPathPersist,
		dirTempPrefix: shared.FolderPrefixPathTemp,
	}
}

type Service struct {
	Options
	listDirPrefix     []string
	listDirTempPrefix []string
	processingMode    uploadconfig.ProcessingMode
}

func Must(opts Options) *Service {
	service, err := New(opts)
	if err != nil {
		panic(err)
	}
	return service
}

// New preserves the historical deep-package constructor for existing consumers.
//
// Deprecated: use NewWithProcessing or the supported host facade. An empty mode
// in NewWithProcessing defaults to originals; this legacy constructor uses media.
func New(opts Options) (*Service, error) {
	return NewWithProcessing(opts, uploadconfig.ProcessingMediaResizer)
}

// NewWithProcessing selects the pipeline once at construction, not from
// untrusted request metadata. The zero mode means original_only.
func NewWithProcessing(opts Options, mode uploadconfig.ProcessingMode) (*Service, error) {
	resolved, err := mode.Resolve()
	if err != nil {
		return nil, err
	}
	if err := opts.Validate(); err != nil {
		return nil, fmt.Errorf("validate options: %w", err)
	}

	defaultPrefix := strings.Split(strings.Trim(opts.dirPrefix.String(), "/"), "/")
	prefix, err := filesanitize.SanitizeSegments(defaultPrefix)
	if err != nil {
		return nil, fmt.Errorf("sanitize persist dir prefix: %w", err)
	}

	defaultTempPrefix := strings.Split(strings.Trim(opts.dirTempPrefix.String(), "/"), "/")
	prefixTemp, err := filesanitize.SanitizeSegments(defaultTempPrefix)
	if err != nil {
		return nil, fmt.Errorf("sanitize temp dir prefix: %w", err)
	}

	if len(opts.validators) > 0 {
		opts.validators = append([]UploadValidator(nil), opts.validators...)
	}

	return &Service{
		Options:           opts,
		listDirPrefix:     prefix,
		listDirTempPrefix: prefixTemp,
		processingMode:    resolved,
	}, nil
}

func (s *Service) UploadBatch(ctx context.Context, req BatchRequest) ([]model.File, error) {
	if len(req.FileHeaders) == 0 {
		return nil, ErrNoFiles
	}

	data := make([]model.File, 0, len(req.FileHeaders))
	for idx, file := range req.FileHeaders {
		res, err := s.UploadSingle(ctx, SingleRequest{
			UploaderUUID: req.UploaderUUID,
			ManagerID:    req.ManagerID,
			UserID:       req.UserID,
			FileHeader:   file,
			ObjectType:   req.ObjectType,
			ObjectID:     req.ObjectID,
			DeletedID:    req.DeletedID,
			AfterJobs:    req.AfterJobs,
			Config:       req.Config,
		})
		if err != nil {
			return nil, fmt.Errorf("upload file [%d idx]: %w", idx, err)
		}

		data = append(data, res)
	}

	return data, nil
}

func (s *Service) UploadSingle(ctx context.Context, req SingleRequest) (model.File, error) {
	if err := req.Validate(); err != nil {
		return model.File{}, fmt.Errorf("validate request: %w", err)
	}
	if err := s.validateReplacement(ctx, req.ObjectType, req.ObjectID, req.DeletedID); err != nil {
		return model.File{}, err
	}

	uploadConfig, err := s.prepareConfig(req)
	if err != nil {
		return model.File{}, fmt.Errorf("prepare upload config: %w", err)
	}

	uploadedFile, err := s.ProcessSingleFileUpload(ctx, req.FileHeader, uploadConfig)
	if err != nil {
		var uErr *uploadError
		if errors.As(err, &uErr) {
			return model.File{}, ClientError{Message: uErr.Error()}
		}
		return model.File{}, fmt.Errorf("process single file upload: %w", err)
	}

	userUploader, err := s.buildUploader(req)
	if err != nil {
		return model.File{}, fmt.Errorf("build uploader: %w", err)
	}
	var fileResult model.File
	err = s.txManager.RunInTx(ctx, func(ctx context.Context) error {
		fileModel, err := s.uploadFile(ctx, req, uploadedFile, userUploader, uploadConfig)
		if err != nil {
			return err
		}
		fileResult = fileModel
		return nil
	})
	if err != nil {
		return model.File{}, fmt.Errorf("enqueue upload task: %w", err)
	}

	return fileResult, nil
}

// UploadReader handles uploads where file content is provided via a reader (e.g. TUS).
func (s *Service) UploadReader(ctx context.Context, req ReaderRequest, input ReaderUploadInput) (model.File, error) {
	if err := req.Validate(); err != nil {
		return model.File{}, fmt.Errorf("validate request: %w", err)
	}
	if err := s.validateReplacement(ctx, req.ObjectType, req.ObjectID, req.DeletedID); err != nil {
		return model.File{}, err
	}

	uploadConfig, err := s.prepareConfig(SingleRequest{
		ObjectType: req.ObjectType,
		ObjectID:   req.ObjectID,
		Config:     req.Config,
	})
	if err != nil {
		return model.File{}, fmt.Errorf("prepare upload config: %w", err)
	}

	uploadedFile, err := s.ProcessReaderUpload(ctx, input, uploadConfig)
	if err != nil {
		var uErr *uploadError
		if errors.As(err, &uErr) {
			return model.File{}, ClientError{Message: uErr.Error()}
		}
		return model.File{}, fmt.Errorf("process reader upload: %w", err)
	}

	singleReq := SingleRequest{
		UploaderUUID: req.UploaderUUID,
		ManagerID:    req.ManagerID,
		UserID:       req.UserID,
		ObjectType:   req.ObjectType,
		ObjectID:     req.ObjectID,
		DeletedID:    req.DeletedID,
		AfterJobs:    req.AfterJobs,
		Config:       req.Config,
	}

	userUploader, err := s.buildUploader(singleReq)
	if err != nil {
		return model.File{}, fmt.Errorf("build uploader: %w", err)
	}

	var fileResult model.File
	err = s.txManager.RunInTx(ctx, func(ctx context.Context) error {
		fileModel, err := s.uploadFile(ctx, singleReq, uploadedFile, userUploader, uploadConfig)
		if err != nil {
			return err
		}
		fileResult = fileModel
		return nil
	})
	if err != nil {
		return model.File{}, fmt.Errorf("enqueue upload task: %w", err)
	}

	return fileResult, nil
}

// UploadStored handles uploads where data is already stored in temp storage (e.g. S3 multipart).
func (s *Service) UploadStored(ctx context.Context, req ReaderRequest, uploaded UploadedFile) (model.File, error) {
	if err := req.Validate(); err != nil {
		return model.File{}, fmt.Errorf("validate request: %w", err)
	}
	if err := s.validateReplacement(ctx, req.ObjectType, req.ObjectID, req.DeletedID); err != nil {
		return model.File{}, err
	}

	if strings.TrimSpace(uploaded.FileName) == "" || strings.TrimSpace(uploaded.OriginalName) == "" {
		return model.File{}, errors.New("invalid uploaded file metadata")
	}

	if uploaded.FileType == model.FileTypeUnknown {
		fileType, err := model.GetFileTypeFromMimeType(uploaded.MimeType)
		if err != nil {
			return model.File{}, fmt.Errorf("detect file type: %w", err)
		}
		uploaded.FileType = fileType
	}

	if uploaded.FolderPath == "" {
		uploaded.FolderPath = filesanitize.EnsureRelativeDir(uploaded.Path)
	}

	uploadConfig, err := s.prepareConfig(SingleRequest{
		ObjectType: req.ObjectType,
		ObjectID:   req.ObjectID,
		Config:     req.Config,
	})
	if err != nil {
		return model.File{}, fmt.Errorf("prepare upload config: %w", err)
	}

	singleReq := SingleRequest{
		UploaderUUID: req.UploaderUUID,
		ManagerID:    req.ManagerID,
		UserID:       req.UserID,
		ObjectType:   req.ObjectType,
		ObjectID:     req.ObjectID,
		DeletedID:    req.DeletedID,
		AfterJobs:    req.AfterJobs,
		Config:       req.Config,
	}

	userUploader, err := s.buildUploader(singleReq)
	if err != nil {
		return model.File{}, fmt.Errorf("build uploader: %w", err)
	}

	var fileResult model.File
	err = s.txManager.RunInTx(ctx, func(ctx context.Context) error {
		fileModel, err := s.uploadFile(ctx, singleReq, uploaded, userUploader, uploadConfig)
		if err != nil {
			return err
		}
		fileResult = fileModel
		return nil
	})
	if err != nil {
		return model.File{}, fmt.Errorf("enqueue upload task: %w", err)
	}

	return fileResult, nil
}

func (s *Service) DeleteFile(ctx context.Context, req DeleteRequest) error {
	if req.FileID <= 0 {
		return fmt.Errorf("invalid file id: %d", req.FileID)
	}

	if req.UserRequestID.IsZero() {
		return errors.New("invalid user request id")
	}

	file, err := s.fileRepo.GetByID(ctx, req.FileID)
	if err != nil {
		return fmt.Errorf("get file by id: %w", err)
	}

	if file.ID == 0 {
		return nil
	}

	payloadObj := deletedfilejob.NewPayload(file.ID, req.UserRequestID, file.GetFullPath(), req.AfterJobs...)
	payload, err := deletedfilejob.MarshalPayload(payloadObj)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	if _, err := s.outbox.Put(ctx, deletedfilejob.JobName, payload, time.Now()); err != nil {
		return fmt.Errorf("put outbox: %w", err)
	}

	return nil
}

func (s *Service) SetPrimary(ctx context.Context, req SetPrimaryRequest) error {
	if req.FileID <= 0 {
		return fmt.Errorf("invalid file id: %d", req.FileID)
	}
	if req.ObjectID <= 0 {
		return fmt.Errorf("invalid object id: %d", req.ObjectID)
	}
	if err := req.ObjectType.Validate(); err != nil {
		return fmt.Errorf("validate object type: %w", err)
	}

	file, err := s.fileRepo.GetByID(ctx, req.FileID)
	if err != nil {
		return fmt.Errorf("get file by id: %w", err)
	}
	if file.ID == 0 {
		return ErrFileNotFound
	}

	return s.txManager.RunInTx(ctx, func(ctx context.Context) error {
		if err := s.fileRepo.ClearPrimary(ctx, req.ObjectType.String(), req.ObjectID, req.FileID); err != nil {
			return fmt.Errorf("clear primary: %w", err)
		}

		if err := s.fileRepo.SetPrimary(ctx, req.FileID, req.ObjectType.String(), req.ObjectID); err != nil {
			return fmt.Errorf("set primary: %w", err)
		}

		return nil
	})
}

func (s *Service) GetFile(ctx context.Context, fileID int64) (model.File, error) {
	if fileID <= 0 {
		return model.File{}, ErrFileNotFound
	}

	file, err := s.fileRepo.GetByID(ctx, fileID)
	if err != nil {
		return model.File{}, fmt.Errorf("get file by id: %w", err)
	}

	if file.ID == 0 {
		return model.File{}, ErrFileNotFound
	}

	return file, nil
}

func (s *Service) prepareConfig(req SingleRequest) (*FileUploadConfig, error) {
	if s.processingMode != uploadconfig.ProcessingMediaResizer {
		if req.ObjectID <= 0 {
			return nil, ClientError{Message: "original upload requires a positive object id"}
		}
		if err := req.ObjectType.Validate(); err != nil {
			return nil, fmt.Errorf("validate original upload object type: %w", err)
		}
	}
	var cfg FileUploadConfig
	if req.Config != nil {
		cfg = *req.Config
		cfg.AllowedExtensions = append([]string(nil), cfg.AllowedExtensions...)
		cfg.AllowedMimeTypes = cloneMimeMap(cfg.AllowedMimeTypes)
	} else {
		cfg = *DefaultFileUploadConfig()
	}

	prefix := slices.Clone(s.listDirTempPrefix)
	prefixDir, err := s.uploadDir(prefix, req.ObjectType.String(), req.ObjectID.String())
	if err != nil {
		return nil, err
	}
	cfg.UploadDir = prefixDir

	return &cfg, nil
}

func (s *Service) uploadDir(prefix []string, objectType string, objectID string) (string, error) {
	segments := append([]string{}, prefix...)
	if value := strings.TrimSpace(objectType); value != "" {
		segments = append(segments, value)
	}
	if raw := strings.TrimSpace(objectID); raw != "" {
		segments = append(segments, raw)
	}
	segments = append(segments, uuid.NewString())

	return filesanitize.BuildSafePath(segments...)
}

func (s *Service) uploadFile(
	ctx context.Context,
	req SingleRequest,
	uploadedFile UploadedFile,
	userUploader shared.FileUploader,
	config *FileUploadConfig,
) (model.File, error) {
	now := time.Now()
	fileModel := model.File{
		ManagerID:        pointer.To(req.ManagerID),
		UserID:           pointer.To(req.UserID),
		ObjectType:       req.ObjectType,
		ObjectID:         pointer.To(req.ObjectID),
		FolderPath:       uploadedFile.FolderPath,
		URL:              uploadedFile.URL,
		FileName:         uploadedFile.FileName,
		OriginalFileName: uploadedFile.OriginalName,
		MimeType:         uploadedFile.MimeType,
		Size:             uploadedFile.Size,
		FileType:         uploadedFile.FileType,
		Slug:             uuid.New().String(),
		IsPrimary:        false,
		Moderate:         shared.FileModerateStatusDefault,
		Position:         0,
		Data: &model.FileData{
			Width:    uploadedFile.Width,
			Height:   uploadedFile.Height,
			Uploader: userUploader,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	fileID, err := s.fileRepo.Create(ctx, fileModel)
	if err != nil {
		return model.File{}, fmt.Errorf("create upload task: %w", err)
	}
	fileModel.ID = fileID

	if s.processingMode != uploadconfig.ProcessingMediaResizer {
		payload, err := finalizeoriginal.MarshalPayload(finalizeoriginal.Payload{FileID: fileID})
		if err != nil {
			return model.File{}, err
		}
		if _, err := s.outbox.Put(ctx, finalizeoriginal.JobName, payload, now); err != nil {
			return model.File{}, fmt.Errorf("put original finalization job: %w", err)
		}
		return fileModel, nil
	}

	skipResizeVideo := config != nil && config.SkipResizer

	filePath := s.composeSourceURL(uploadedFile)

	payload, err := sendresizefilejob.MarshalPayload(sendresizefilejob.NewPayload(
		fileID,
		filePath,
		skipResizeVideo,
	))
	if err != nil {
		return model.File{}, fmt.Errorf("marshal payload: %w", err)
	}

	if _, err = s.outbox.Put(ctx, sendresizefilejob.JobName, payload, now); err != nil {
		return model.File{}, fmt.Errorf("put outbox job: %w", err)
	}

	return fileModel, nil
}

func (s *Service) buildUploader(req SingleRequest) (shared.FileUploader, error) {
	userUUIDUploader := req.UploaderUUID
	userIDUploader := req.UserID
	userTypeUploader := shared.UserTypeUser
	if req.ManagerID > 0 {
		userIDUploader = req.ManagerID
		userTypeUploader = shared.UserTypeAdmin
	}
	if req.DeletedID > 0 {
		deletedPayload := deletedfilejob.NewOwnedPayload(
			req.DeletedID.Int64(),
			userUUIDUploader,
			req.ObjectType,
			req.ObjectID,
			"",
		)
		deletedPayloadBytes, err := deletedfilejob.MarshalPayload(deletedPayload)
		if err != nil {
			return shared.FileUploader{}, fmt.Errorf("marshal payload: %w", err)
		}
		afterJob := shared.NewFileEventAfterJobsWithPayload(deletedfilejob.JobName, userUUIDUploader, deletedPayloadBytes)
		req.AfterJobs = append(req.AfterJobs, afterJob...)
	}

	return shared.FileUploader{
		UserUUID:  userUUIDUploader,
		UserID:    userIDUploader,
		Type:      userTypeUploader,
		Status:    shared.FileUploadTaskStatusQueued,
		AfterJobs: req.AfterJobs,
	}, nil
}

func (s *Service) validateReplacement(
	ctx context.Context,
	objectType shared.FileObjectType,
	objectID shared.FileObjectID,
	deletedID shared.FileObjectID,
) error {
	if deletedID <= 0 {
		return nil
	}

	replaced, err := s.fileRepo.GetByID(ctx, deletedID.Int64())
	if err != nil {
		return fmt.Errorf("get replacement file %d: %w", deletedID, err)
	}
	if replaced.ID == 0 || replaced.ObjectID == nil ||
		replaced.ObjectType != objectType || *replaced.ObjectID != objectID {
		return ClientError{Message: "replacement file does not belong to the upload object"}
	}

	return nil
}

func (s *Service) composeSourceURL(uploadedFile UploadedFile) string {
	if path := strings.TrimSpace(uploadedFile.Path); path != "" {
		return path
	}

	return uploadedFile.URL
}

func cloneMimeMap(src map[string][]string) map[string][]string {
	if len(src) == 0 {
		return nil
	}

	clone := make(map[string][]string, len(src))
	for key, values := range src {
		clone[strings.ToLower(key)] = append([]string(nil), values...)
	}
	return clone
}
