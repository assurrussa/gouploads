package host

import (
	"errors"

	logger "github.com/assurrussa/gologger"
	"github.com/assurrussa/outbox/outbox"

	outboxdeleted "github.com/assurrussa/gouploads/domain/files/outbox/deleted_file"
	finalizeoriginal "github.com/assurrussa/gouploads/domain/files/outbox/finalize_original"
	outboxlistenresize "github.com/assurrussa/gouploads/domain/files/outbox/listen_resize_file"
	outboxsendresize "github.com/assurrussa/gouploads/domain/files/outbox/send_resize_file"
	outboxupload "github.com/assurrussa/gouploads/domain/files/outbox/upload_file"
	deletefileuc "github.com/assurrussa/gouploads/domain/files/usecases/command/delete_file"
	sendresizeuc "github.com/assurrussa/gouploads/domain/files/usecases/command/send_resize_file"
	uploadfileuc "github.com/assurrussa/gouploads/domain/files/usecases/command/upload_file"
)

type (
	DeleteFileCommand       = deletefileuc.UseCase
	SendResizeCommand       = sendresizeuc.UseCase
	UploadFileCommand       = uploadfileuc.UseCase
	FinalizeOriginalCommand = uploadfileuc.OriginalUseCase
)

type OutboxJobDeps struct {
	Logger                  logger.Logger
	UseCaseSendResize       *SendResizeCommand
	UseCaseListenResize     *ListenResizeCommand
	UseCaseUploadFile       *UploadFileCommand
	UseCaseDeleteFile       *DeleteFileCommand
	UseCaseFinalizeOriginal *FinalizeOriginalCommand
}

// OutboxJobs preserves the historical fail-fast constructor. New integrations
// may use BuildOutboxJobs to handle invalid wiring without a panic.
func OutboxJobs(deps OutboxJobDeps) []outbox.Job {
	jobs, err := BuildOutboxJobs(deps)
	if err != nil {
		panic(err)
	}
	return jobs
}

// BuildOutboxJobs accepts originals, the complete media pipeline, or both for
// draining old jobs during migration. It never creates a missing media service.
func BuildOutboxJobs(deps OutboxJobDeps) ([]outbox.Job, error) {
	if deps.Logger == nil || deps.UseCaseDeleteFile == nil {
		return nil, errors.New("upload jobs require logger and delete command")
	}
	media := deps.UseCaseSendResize != nil || deps.UseCaseListenResize != nil || deps.UseCaseUploadFile != nil
	if media && (deps.UseCaseSendResize == nil || deps.UseCaseListenResize == nil || deps.UseCaseUploadFile == nil) {
		return nil, errors.New("media jobs require send, listen and upload commands together")
	}
	if !media && deps.UseCaseFinalizeOriginal == nil {
		return nil, errors.New("upload jobs require an original or media finalizer")
	}
	jobs := make([]outbox.Job, 0, 5)
	if media {
		jobs = append(jobs,
			outboxsendresize.Must(outboxsendresize.NewOptions(deps.UseCaseSendResize, deps.Logger)),
			outboxlistenresize.Must(outboxlistenresize.NewOptions(deps.UseCaseListenResize, deps.Logger)),
			outboxupload.Must(outboxupload.NewOptions(deps.UseCaseUploadFile, deps.Logger)),
		)
	}
	if deps.UseCaseFinalizeOriginal != nil {
		job, err := finalizeoriginal.New(deps.UseCaseFinalizeOriginal)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	jobs = append(jobs, outboxdeleted.Must(outboxdeleted.NewOptions(deps.UseCaseDeleteFile, deps.Logger)))
	return jobs, nil
}
