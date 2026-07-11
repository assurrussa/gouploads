package host

import (
	pgsql "github.com/assurrussa/outbox/backends/pgsql/storage"

	uploadmodel "github.com/assurrussa/gouploads/domain/files/model"
	"github.com/assurrussa/gouploads/domain/files/repositories/filerepo"
	eventfileafterprocess "github.com/assurrussa/gouploads/domain/files/service/event_file_after_process"
	"github.com/assurrussa/gouploads/domain/files/service/fileloader"
	"github.com/assurrussa/gouploads/domain/files/service/tusupload"
	"github.com/assurrussa/gouploads/domain/files/service/uploadservice"
	fileshared "github.com/assurrussa/gouploads/domain/files/shared"
	filestorage "github.com/assurrussa/gouploads/infrastructure/storage/files"
)

var (
	ErrTusNotFound        = tusupload.ErrNotFound
	ErrTusOffsetMismatch  = tusupload.ErrOffsetMismatch
	ErrTusLengthExceeded  = tusupload.ErrLengthExceeded
	ErrTusChunkTooSmall   = tusupload.ErrChunkTooSmall
	ErrTusChunkSize       = tusupload.ErrChunkSize
	ErrTusUploadBusy      = tusupload.ErrUploadBusy
	ErrTusFenceLost       = tusupload.ErrFenceLost
	ErrTusUploadFinalized = tusupload.ErrUploadFinalized
)

type (
	AfterProcessService   = eventfileafterprocess.Service
	AfterProcessFunc      = eventfileafterprocess.FnCallAfterProcess
	AfterProcessPayload   = eventfileafterprocess.Payload
	BatchRequest          = uploadservice.BatchRequest
	ClientError           = uploadservice.ClientError
	DeleteRequest         = uploadservice.DeleteRequest
	EventAfterProcess     = fileshared.EventAfterProcess
	File                  = uploadmodel.File
	FileData              = uploadmodel.FileData
	FileDeletedEvent      = fileshared.FileDeletedEvent
	FileDeleteStatus      = fileshared.FileDeleteStatus
	FileEventAfterJob     = fileshared.FileEventAfterJob
	FileLoader            = fileloader.Service
	FilePreset            = fileshared.FilePreset
	FileUploadEventFile   = fileshared.FileUploadEventFile
	FileUploadStatusEvent = fileshared.FileUploadStatusEvent
	FileRepo              = filerepo.Repo
	FileUploadConfig      = uploadservice.FileUploadConfig
	FileUploadTaskStatus  = fileshared.FileUploadTaskStatus
	ListFilters           = filerepo.ListFilters
	ObjectID              = fileshared.FileObjectID
	ObjectType            = fileshared.FileObjectType
	PresetName            = fileshared.PresetName
	ReaderRequest         = uploadservice.ReaderRequest
	ReaderUploadInput     = uploadservice.ReaderUploadInput
	SingleRequest         = uploadservice.SingleRequest
	Status                = fileshared.Status
	TusStore              = tusupload.Store
	TusCreateRequest      = tusupload.CreateRequest
	TusSession            = tusupload.Session
	TusCompleteResult     = tusupload.CompleteResult
	TusStatus             = tusupload.Status
	UploadedFile          = uploadservice.UploadedFile
	UploadService         = uploadservice.Service
	UploadValidator       = uploadservice.UploadValidator
	UploadValidatorFunc   = uploadservice.UploadValidatorFunc
	UserType              = fileshared.UserType
	ValidationError       = uploadservice.ValidationError
	Storage               = filestorage.Storage
	SaveFileInput         = filestorage.SaveFileInput
	CommitInput           = filestorage.CommitInput
	StoredFile            = filestorage.StoredFile
	ExistFileInput        = filestorage.ExistFileInput
	ExistFile             = filestorage.ExistFile
)

const (
	EventTypeAfterProcess                               = fileshared.EventTypeAfterProcess
	EventTypeDeleted                                    = fileshared.EventTypeDeleted
	EventTypeUploadStatus                               = fileshared.EventTypeUploadStatus
	FileDeleteStatusCompleted      FileDeleteStatus     = fileshared.FileDeleteStatusCompleted
	FileDeleteStatusFailed         FileDeleteStatus     = fileshared.FileDeleteStatusFailed
	FileUploadTaskStatusCompleted  FileUploadTaskStatus = fileshared.FileUploadTaskStatusCompleted
	FileUploadTaskStatusFailed     FileUploadTaskStatus = fileshared.FileUploadTaskStatusFailed
	FileUploadTaskStatusProcessing FileUploadTaskStatus = fileshared.FileUploadTaskStatusProcessing
	FileUploadTaskStatusQueued     FileUploadTaskStatus = fileshared.FileUploadTaskStatusQueued
	ObjectTypeAdmin                ObjectType           = fileshared.ObjectTypeAdmin
	ObjectTypeBonusLesson          ObjectType           = fileshared.ObjectTypeBonusLesson
	ObjectTypeExercise             ObjectType           = fileshared.ObjectTypeExercise
	ObjectTypeKnowledgeBase        ObjectType           = fileshared.ObjectTypeKnowledgeBase
	StatusCompleted                Status               = fileshared.StatusCompleted
	StatusFailed                   Status               = fileshared.StatusFailed
	UserTypeAdmin                  UserType             = fileshared.UserTypeAdmin
	UserTypeUser                   UserType             = fileshared.UserTypeUser
	TusStatusActive                TusStatus            = tusupload.StatusActive
	TusStatusFinalizing            TusStatus            = tusupload.StatusFinalizing
	TusStatusReady                 TusStatus            = tusupload.StatusReady
)

func NewTusStore(cfg StorageConfig, database pgsql.Client) (TusStore, error) {
	return tusupload.BuildDurableStore(cfg, database)
}

func ObjectIDPtr(id int64) *ObjectID {
	objectID := ObjectID(id)
	return &objectID
}

func NewFileRepo(client pgsql.Client, tx pgsql.TxManager) (*FileRepo, error) {
	return filerepo.New(filerepo.NewOptions(client, tx))
}

func MustFileRepo(client pgsql.Client, tx pgsql.TxManager) *FileRepo {
	return filerepo.Must(filerepo.NewOptions(client, tx))
}

func NewAfterProcessPayload(
	userID UserID,
	userType UserType,
	fileID int64,
	eventType string,
	metas ...map[string]any,
) AfterProcessPayload {
	return eventfileafterprocess.NewPayload(userID, userType, fileID, eventType, metas...)
}

func MarshalAfterProcessPayload(payload AfterProcessPayload) (string, error) {
	return eventfileafterprocess.MarshalPayload(payload)
}

func UnmarshalAfterProcessPayload(data string) (AfterProcessPayload, error) {
	return eventfileafterprocess.UnmarshalPayload(data)
}

func NewFileUploadStatusEvent(taskID int64, status FileUploadTaskStatus) FileUploadStatusEvent {
	return fileshared.NewFileUploadStatusEvent(taskID, status)
}

func NewEventAfterProcess(fileID int64, filePath string, status Status, eventTrigger string) EventAfterProcess {
	return fileshared.NewEventAfterProcess(fileID, filePath, status, eventTrigger)
}

func NewFileDeletedEvent(fileID int64, filePath string, status FileDeleteStatus) FileDeletedEvent {
	return fileshared.NewFileDeletedEvent(fileID, filePath, status)
}

func NewFileEventAfterJob(jobName string, userID UserID, payload string, metas ...map[string]any) FileEventAfterJob {
	return fileshared.NewFileEventAfterJob(jobName, userID, payload, metas...)
}

func NewFileEventAfterJobs(jobName string, userID UserID, metas ...map[string]any) []FileEventAfterJob {
	return fileshared.NewFileEventAfterJobs(jobName, userID, metas...)
}

func NewFileEventAfterJobsWithPayload(jobName string, userID UserID, payload string) []FileEventAfterJob {
	return fileshared.NewFileEventAfterJobsWithPayload(jobName, userID, payload)
}
