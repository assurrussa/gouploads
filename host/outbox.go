package host

import (
	logger "github.com/assurrussa/gologger"
	"github.com/assurrussa/outbox/outbox"

	outboxdeleted "github.com/assurrussa/gouploads/domain/files/outbox/deleted_file"
	outboxlistenresize "github.com/assurrussa/gouploads/domain/files/outbox/listen_resize_file"
	outboxsendresize "github.com/assurrussa/gouploads/domain/files/outbox/send_resize_file"
	outboxupload "github.com/assurrussa/gouploads/domain/files/outbox/upload_file"
	deletefileuc "github.com/assurrussa/gouploads/domain/files/usecases/command/delete_file"
	sendresizeuc "github.com/assurrussa/gouploads/domain/files/usecases/command/send_resize_file"
	uploadfileuc "github.com/assurrussa/gouploads/domain/files/usecases/command/upload_file"
)

type (
	DeleteFileCommand = deletefileuc.UseCase
	SendResizeCommand = sendresizeuc.UseCase
	UploadFileCommand = uploadfileuc.UseCase
)

type OutboxJobDeps struct {
	Logger              logger.Logger
	UseCaseSendResize   *SendResizeCommand
	UseCaseListenResize *ListenResizeCommand
	UseCaseUploadFile   *UploadFileCommand
	UseCaseDeleteFile   *DeleteFileCommand
}

func OutboxJobs(deps OutboxJobDeps) []outbox.Job {
	return []outbox.Job{
		outboxsendresize.Must(outboxsendresize.NewOptions(deps.UseCaseSendResize, deps.Logger)),
		outboxlistenresize.Must(outboxlistenresize.NewOptions(deps.UseCaseListenResize, deps.Logger)),
		outboxupload.Must(outboxupload.NewOptions(deps.UseCaseUploadFile, deps.Logger)),
		outboxdeleted.Must(outboxdeleted.NewOptions(deps.UseCaseDeleteFile, deps.Logger)),
	}
}
