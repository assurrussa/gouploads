package host

import (
	uploadmodel "github.com/assurrussa/gouploads/domain/files/model"
	"github.com/assurrussa/gouploads/domain/files/repositories/filerepo"
	eventfileafterprocess "github.com/assurrussa/gouploads/domain/files/service/event_file_after_process"
	"github.com/assurrussa/gouploads/domain/files/service/fileloader"
	"github.com/assurrussa/gouploads/domain/files/service/tusupload"
	"github.com/assurrussa/gouploads/domain/files/service/uploadservice"
	fileshared "github.com/assurrussa/gouploads/domain/files/shared"
)

type (
	AfterProcessService  = eventfileafterprocess.Service
	BatchRequest         = uploadservice.BatchRequest
	ClientError          = uploadservice.ClientError
	DeleteRequest        = uploadservice.DeleteRequest
	EventAfterProcess    = fileshared.EventAfterProcess
	File                 = uploadmodel.File
	FileData             = uploadmodel.FileData
	FileEventAfterJob    = fileshared.FileEventAfterJob
	FileLoader           = fileloader.Service
	FilePreset           = fileshared.FilePreset
	FileRepo             = filerepo.Repo
	FileUploadConfig     = uploadservice.FileUploadConfig
	FileUploadTaskStatus = fileshared.FileUploadTaskStatus
	ListFilters          = filerepo.ListFilters
	ObjectID             = fileshared.FileObjectID
	ObjectType           = fileshared.FileObjectType
	PresetName           = fileshared.PresetName
	ReaderRequest        = uploadservice.ReaderRequest
	ReaderUploadInput    = uploadservice.ReaderUploadInput
	SingleRequest        = uploadservice.SingleRequest
	Status               = fileshared.Status
	TusStore             = tusupload.Store
	UploadedFile         = uploadservice.UploadedFile
	UploadService        = uploadservice.Service
	UploadValidator      = uploadservice.UploadValidator
	UploadValidatorFunc  = uploadservice.UploadValidatorFunc
	UserType             = fileshared.UserType
	ValidationError      = uploadservice.ValidationError
)

const (
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
)

func ObjectIDPtr(id int64) *ObjectID {
	objectID := ObjectID(id)
	return &objectID
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
