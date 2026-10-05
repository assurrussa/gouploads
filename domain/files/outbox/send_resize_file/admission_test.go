package sendresizefilejob_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/assurrussa/outbox/outbox"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	clientresizer "github.com/assurrussa/gouploads/domain/files/service/client_resizer"
	usecase "github.com/assurrussa/gouploads/domain/files/usecases/command/send_resize_file"
)

func TestAdmissionDisposition(t *testing.T) {
	for _, code := range []int{400, 409, 413, 429, 503} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			ctx, _, ts := NewTestSuite(t)
			sourceErr := &clientresizer.AdmissionError{StatusCode: code, RetryAfter: 7 * time.Second}
			ts.useCaseMock.EXPECT().Handle(ctx, gomock.Any()).Return(usecase.Response{}, sourceErr)
			before := time.Now()
			err := ts.job.Handle(ctx, `{"fileId":1,"filePath":"source.png"}`)
			require.ErrorIs(t, err, sourceErr)
			require.Equal(t, code < 429, outbox.IsPermanent(err))
			retryAt, ok := outbox.RetryTime(err)
			require.Equal(t, code >= 429, ok)
			if ok {
				require.WithinDuration(t, before.Add(7*time.Second), retryAt, time.Second)
			}
		})
	}
}
