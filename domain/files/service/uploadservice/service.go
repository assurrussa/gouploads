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
	"github.com/assurrussa/gouploads/internal/filejobs"
	"github.com/assurrussa/gouploads/internal/filepolicy"
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
	return Options{dirPrefix: shared.FolderPrefixPathPersist, dirTempPrefix: shared.FolderPrefixPathTemp}
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

// New preserves the historical deep-package constructor.
//
// Deprecated: use NewWithProcessing or the supported host facade.
func New(opts Options) (*Service, error) {
	return NewWithProcessing(opts, uploadconfig.ProcessingMediaResizer)
}

func NewWithProcessing(opts Options, mode uploadconfig.ProcessingMode) (*Service, error) {
	resolved, err := mode.Resolve()
	if err != nil {
		return nil, err
	}
	if err := opts.Validate(); err != nil {
		return nil, fmt.Errorf("validate options: %w", err)
	}
	prefix, err := filesanitize.SanitizeSegments(strings.Split(strings.Trim(opts.dirPrefix.String(), "/"), "/"))
	if err != nil {
		return nil, fmt.Errorf("sanitize persist dir prefix: %w", err)
	}
	temp, err := filesanitize.SanitizeSegments(strings.Split(strings.Trim(opts.dirTempPrefix.String(), "/"), "/"))
	if err != nil {
		return nil, fmt.Errorf("sanitize temp dir prefix: %w", err)
	}
	opts.validators = append([]UploadValidator(nil), opts.validators...)
	return &Service{Options: opts, listDirPrefix: prefix, listDirTempPrefix: temp, processingMode: resolved}, nil
}

// UploadBatch is intentionally non-atomic. On failure it returns the successful
// prefix and a BatchError; callers must not blindly retry that prefix.
func (s *Service) UploadBatch(ctx context.Context, req BatchRequest) ([]model.File, error) {
	if len(req.FileHeaders) == 0 {
		return nil, ErrNoFiles
	}
	data := make([]model.File, 0, len(req.FileHeaders))
	for index, header := range req.FileHeaders {
		file, err := s.UploadSingle(ctx, SingleRequest{
			UploaderUUID: req.UploaderUUID, ManagerID: req.ManagerID, UserID: req.UserID, FileHeader: header,
			ObjectType: req.ObjectType, ObjectID: req.ObjectID, DeletedID: req.DeletedID, AfterJobs: req.AfterJobs, Config: req.Config,
		})
		if err != nil {
			if len(data) == 0 {
				data = nil
			}
			return data, &BatchError{FailedIndex: index, Err: err}
		}
		data = append(data, file)
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
	cfg, err := s.prepareConfig(req)
	if err != nil {
		return model.File{}, fmt.Errorf("prepare upload config: %w", err)
	}
	uploader, err := s.buildUploader(req)
	if err != nil {
		return model.File{}, fmt.Errorf("build uploader: %w", err)
	}
	uploaded, err := s.ProcessSingleFileUpload(ctx, req.FileHeader, cfg)
	if err != nil {
		return model.File{}, uploadClientError(err)
	}
	return s.persistUpload(ctx, req, uploaded, uploader, cfg)
}

func (s *Service) UploadReader(ctx context.Context, req ReaderRequest, input ReaderUploadInput) (model.File, error) {
	if req.FinalizationKey != "" {
		return s.withFinalization(ctx, req, input.OriginalName, input.Size, false, func(ctx context.Context) (model.File, error) {
			req.FinalizationKey = ""
			return s.UploadReader(ctx, req, input)
		})
	}
	if err := req.Validate(); err != nil {
		return model.File{}, fmt.Errorf("validate request: %w", err)
	}
	if err := s.validateReplacement(ctx, req.ObjectType, req.ObjectID, req.DeletedID); err != nil {
		return model.File{}, err
	}
	single := singleReaderRequest(req)
	cfg, err := s.prepareConfig(single)
	if err != nil {
		return model.File{}, fmt.Errorf("prepare upload config: %w", err)
	}
	uploader, err := s.buildUploader(single)
	if err != nil {
		return model.File{}, fmt.Errorf("build uploader: %w", err)
	}
	uploaded, err := s.ProcessReaderUpload(ctx, input, cfg)
	if err != nil {
		return model.File{}, uploadClientError(err)
	}
	return s.persistUpload(ctx, single, uploaded, uploader, cfg)
}

func (s *Service) UploadStored(ctx context.Context, req ReaderRequest, uploaded UploadedFile) (model.File, error) {
	if req.FinalizationKey != "" {
		return s.withFinalization(ctx, req, uploaded.OriginalName, uploaded.Size, true, func(ctx context.Context) (model.File, error) {
			req.FinalizationKey = ""
			return s.UploadStored(ctx, req, uploaded)
		})
	}
	if err := req.Validate(); err != nil {
		return model.File{}, fmt.Errorf("validate request: %w", err)
	}
	if err := s.validateReplacement(ctx, req.ObjectType, req.ObjectID, req.DeletedID); err != nil {
		return model.File{}, err
	}
	single := singleReaderRequest(req)
	cfg, err := s.prepareConfig(single)
	if err != nil {
		return model.File{}, fmt.Errorf("prepare upload config: %w", err)
	}
	if err := s.validateStoredUpload(ctx, &uploaded, cfg); err != nil {
		return model.File{}, uploadClientError(err)
	}
	uploader, err := s.buildUploader(single)
	if err != nil {
		return model.File{}, fmt.Errorf("build uploader: %w", err)
	}
	return s.persistUpload(ctx, single, uploaded, uploader, cfg)
}

func singleReaderRequest(req ReaderRequest) SingleRequest {
	return SingleRequest{
		UploaderUUID: req.UploaderUUID, ManagerID: req.ManagerID, UserID: req.UserID,
		ObjectType: req.ObjectType, ObjectID: req.ObjectID, DeletedID: req.DeletedID, AfterJobs: req.AfterJobs, Config: req.Config,
	}
}

func uploadClientError(err error) error {
	var validation *uploadError
	if errors.As(err, &validation) {
		return ClientError{Message: validation.Error()}
	}
	return err
}

func (s *Service) persistUpload(
	ctx context.Context,
	req SingleRequest,
	uploaded UploadedFile,
	uploader shared.FileUploader,
	cfg *FileUploadConfig,
) (model.File, error) {
	var result model.File
	err := s.txManager.RunInTx(ctx, func(ctx context.Context) error {
		var err error
		result, err = s.uploadFile(ctx, req, uploaded, uploader, cfg)
		return err
	})
	if err != nil {
		// An ambiguous commit must not destroy a source a committed job may need.
		return model.File{}, fmt.Errorf("enqueue upload task: %w", err)
	}
	return result, nil
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
	payload, err := deletedfilejob.MarshalPayload(deletedfilejob.NewPayload(file.ID,
		req.UserRequestID,
		file.GetFullPath(),
		req.AfterJobs...))
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	if _, err := filejobs.Put(ctx, s.outbox, file, model.FileJobDeletion, deletedfilejob.JobName, payload, time.Now()); err != nil {
		return fmt.Errorf("put outbox: %w", err)
	}
	return nil
}

func (s *Service) SetPrimary(ctx context.Context, req SetPrimaryRequest) error {
	if req.FileID <= 0 || req.ObjectID <= 0 {
		return errors.New("positive file and object IDs are required")
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
	if file.ObjectID == nil || file.ObjectType != req.ObjectType || file.ObjectID.Int64() != req.ObjectID {
		return model.ErrObjectBindingMismatch
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

func (s *Service) GetFile(ctx context.Context, id int64) (model.File, error) {
	if id <= 0 {
		return model.File{}, ErrFileNotFound
	}
	file, err := s.fileRepo.GetByID(ctx, id)
	if err != nil {
		return model.File{}, fmt.Errorf("get file by id: %w", err)
	}
	if file.ID == 0 {
		return model.File{}, ErrFileNotFound
	}
	return file, nil
}

// ValidateUploadConfig checks an effective policy against this uploader's
// processing mode without accepting bytes or creating an upload. The caller must
// resolve defaults first; nil is invalid. It does not merge or mutate cfg.
// Admission never replaces ingestion or finalization content validation.
func (s *Service) ValidateUploadConfig(cfg *FileUploadConfig) error {
	if cfg == nil {
		return errors.New("upload config is required")
	}
	if cfg.MaxFileSize < 0 {
		return errors.New("upload size limit must not be negative")
	}
	if s.processingMode != uploadconfig.ProcessingMediaResizer {
		return filepolicy.ValidateOriginalConfig(cfg.MaxFileSize, cfg.AllowedExtensions, cfg.AllowedMimeTypes)
	}
	return nil
}

func (s *Service) prepareConfig(req SingleRequest) (*FileUploadConfig, error) {
	if req.Config != nil && req.Config.MaxFileSize < 0 {
		return nil, errors.New("upload size limit must not be negative")
	}
	cfg := s.mergeConfig(req.Config)
	if err := s.ValidateUploadConfig(cfg); err != nil {
		return nil, err
	}
	if s.processingMode != uploadconfig.ProcessingMediaResizer {
		if req.ObjectID <= 0 {
			return nil, ClientError{Message: "original upload requires a positive object id"}
		}
		if err := req.ObjectType.Validate(); err != nil {
			return nil, fmt.Errorf("validate original object type: %w", err)
		}
	}
	prefixDir, err := s.uploadDir(slices.Clone(s.listDirTempPrefix), req.ObjectType.String(), req.ObjectID.String())
	if err != nil {
		return nil, err
	}
	cfg.UploadDir = prefixDir
	return cfg, nil
}

func (s *Service) uploadDir(prefix []string, objectType, objectID string) (string, error) {
	segments := append([]string(nil), prefix...)
	if value := strings.TrimSpace(objectType); value != "" {
		segments = append(segments, value)
	}
	if value := strings.TrimSpace(objectID); value != "" {
		segments = append(segments, value)
	}
	segments = append(segments, uuid.NewString())
	return filesanitize.BuildSafePath(segments...)
}

func (s *Service) uploadFile(
	ctx context.Context,
	req SingleRequest,
	uploaded UploadedFile,
	uploader shared.FileUploader,
	cfg *FileUploadConfig,
) (model.File, error) {
	now := time.Now()
	file := model.File{
		ManagerID:  pointer.To(req.ManagerID),
		UserID:     pointer.To(req.UserID),
		ObjectType: req.ObjectType,
		ObjectID:   pointer.To(req.ObjectID),

		FolderPath: uploaded.FolderPath, URL: uploaded.URL, FileName: uploaded.FileName, OriginalFileName: uploaded.OriginalName,
		MimeType: uploaded.MimeType, Size: uploaded.Size, FileType: uploaded.FileType, Slug: uuid.NewString(),
		Moderate: shared.FileModerateStatusDefault,
		Data: &model.FileData{
			Width:    uploaded.Width,
			Height:   uploaded.Height,
			Uploader: uploader,
		},

		CreatedAt: now, UpdatedAt: now,
	}
	id, err := s.fileRepo.Create(ctx, file)
	if err != nil {
		return model.File{}, fmt.Errorf("create upload task: %w", err)
	}
	file.ID = id
	if s.processingMode != uploadconfig.ProcessingMediaResizer {
		payload, err := finalizeoriginal.MarshalPayload(finalizeoriginal.Payload{FileID: id})
		if err != nil {
			return model.File{}, err
		}
		if _, err := filejobs.Put(ctx, s.outbox, file, model.FileJobOriginalFinalization, finalizeoriginal.JobName, payload, now); err != nil {
			return model.File{}, fmt.Errorf("put original finalization job: %w", err)
		}
		return file, nil
	}
	skip := cfg != nil && cfg.SkipResizer
	payload, err := sendresizefilejob.MarshalPayload(sendresizefilejob.NewPayload(id, s.composeSourceURL(uploaded), skip))
	if err != nil {
		return model.File{}, fmt.Errorf("marshal payload: %w", err)
	}
	if _, err := filejobs.Put(ctx, s.outbox, file, model.FileJobMediaAdmission, sendresizefilejob.JobName, payload, now); err != nil {
		return model.File{}, fmt.Errorf("put outbox job: %w", err)
	}
	return file, nil
}

func (s *Service) buildUploader(req SingleRequest) (shared.FileUploader, error) {
	id, kind := req.UserID, shared.UserTypeUser
	if req.ManagerID > 0 {
		id, kind = req.ManagerID, shared.UserTypeAdmin
	}
	afterJobs := append([]shared.FileEventAfterJob(nil), req.AfterJobs...)
	if req.DeletedID > 0 {
		payload, err := deletedfilejob.MarshalPayload(deletedfilejob.NewOwnedPayload(req.DeletedID.Int64(),
			req.UploaderUUID,
			req.ObjectType,
			req.ObjectID,
			""))
		if err != nil {
			return shared.FileUploader{}, fmt.Errorf("marshal payload: %w", err)
		}
		afterJobs = append(afterJobs, shared.NewFileEventAfterJobsWithPayload(deletedfilejob.JobName, req.UploaderUUID, payload)...)
	}
	return shared.FileUploader{
		UserUUID:  req.UploaderUUID,
		UserID:    id,
		Type:      kind,
		Status:    shared.FileUploadTaskStatusQueued,
		AfterJobs: afterJobs,
	}, nil
}

func (s *Service) validateReplacement(
	ctx context.Context,
	objectType shared.FileObjectType,
	objectID,
	deletedID shared.FileObjectID,
) error {
	if deletedID <= 0 {
		return nil
	}
	file, err := s.fileRepo.GetByID(ctx, deletedID.Int64())
	if err != nil {
		return fmt.Errorf("get replacement file %d: %w", deletedID, err)
	}
	if file.ID == 0 || file.ObjectID == nil || file.ObjectType != objectType || *file.ObjectID != objectID {
		return ClientError{Message: "replacement file does not belong to the upload object"}
	}
	return nil
}

func (s *Service) composeSourceURL(file UploadedFile) string {
	if value := strings.TrimSpace(file.Path); value != "" {
		return value
	}
	return file.URL
}

func cloneMimeMap(src map[string][]string) map[string][]string {
	if len(src) == 0 {
		return nil
	}
	result := make(map[string][]string, len(src))
	for key, values := range src {
		result[strings.ToLower(strings.TrimSpace(key))] = append([]string(nil), values...)
	}
	return result
}
