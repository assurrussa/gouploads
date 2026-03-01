package ceph

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

const (
	// MinPartSize Минимальный размер одной части при multipart загрузке. Минимальный размер должен быть не меньше 5Мб
	// При multipart загрузке s3 позволяет передавать часть меньше чем 5MiB только в одном случае - если эта часть
	// последняя (или единственная, как частный случай) в multipart сессии.
	MinPartSize = 1024 * 1024 * 5
)

// Upload производит загрузку файла по частям. Является вспомогательным методом и берет все операции и
// обработку краевых случаев на себя.
func Upload(
	ctx context.Context,
	client MultiPartUploader,
	input *s3.CreateMultipartUploadInput,
	reader io.Reader,
	optFns ...func(*s3.Options),
) error {
	mpu, err := client.CreateMultipartUpload(ctx, input, optFns...)
	if err != nil {
		return fmt.Errorf("can't create multipart: %w", err)
	}

	abortUpload := func() error {
		// используется контекст без отмены для того, чтобы в случае завершения основного контекста мы все же смогли
		// прервать операцию загрузки файла
		_, err := client.AbortMultipartUpload(context.WithoutCancel(ctx), &s3.AbortMultipartUploadInput{
			Bucket:   mpu.Bucket,
			Key:      mpu.Key,
			UploadId: mpu.UploadId,
		}, optFns...)

		return err
	}

	buf := make([]byte, MinPartSize)
	mupl := &types.CompletedMultipartUpload{}
	var eof bool
	for partNo := 1; !eof; partNo++ {
		select {
		case <-ctx.Done():
			err := errors.New("context has been canceled before upload has done")
			abortErr := abortUpload()
			if abortErr != nil {
				err = fmt.Errorf("%w: can't abort upload: %w", err, abortErr)
			}
			return err
		default:
		}

		n, err := io.ReadAtLeast(reader, buf[:cap(buf)], MinPartSize)
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
			err := fmt.Errorf("can't read next part #%d of file `%s`: %w", partNo, *input.Key, err)
			abortErr := abortUpload()
			if abortErr != nil {
				err = fmt.Errorf("%w: can't abort upload: %w", err, abortErr)
			}
			return err
		}
		if err != nil {
			eof = true
		}
		if n == 0 {
			break
		}
		buf = buf[:n]

		contentLength := int64(len(buf))
		partNumber := int32(partNo)
		upl, err := client.UploadPart(ctx, &s3.UploadPartInput{
			Body:                 bytes.NewReader(buf),
			Bucket:               mpu.Bucket,
			ContentLength:        &contentLength,
			ExpectedBucketOwner:  input.ExpectedBucketOwner,
			Key:                  mpu.Key,
			PartNumber:           &partNumber,
			RequestPayer:         input.RequestPayer,
			SSECustomerAlgorithm: input.SSECustomerAlgorithm,
			SSECustomerKey:       input.SSECustomerKey,
			SSECustomerKeyMD5:    input.SSECustomerKeyMD5,
			UploadId:             mpu.UploadId,
		}, optFns...)
		if err != nil {
			err := fmt.Errorf("can't upload part #%d of file `%s`: %w", partNo, *input.Key, err)
			abortErr := abortUpload()
			if abortErr != nil {
				err = fmt.Errorf("%w: can't abort upload: %w", err, abortErr)
			}
			return err
		}

		mupl.Parts = append(mupl.Parts, types.CompletedPart{
			ETag:       upl.ETag,
			PartNumber: &partNumber,
		})
	}

	_, err = client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:              mpu.Bucket,
		ExpectedBucketOwner: input.ExpectedBucketOwner,
		Key:                 mpu.Key,
		MultipartUpload:     mupl,
		RequestPayer:        input.RequestPayer,
		UploadId:            mpu.UploadId,
	}, optFns...)
	if err != nil {
		err := fmt.Errorf("can't complete multipart upload of file `%s`: %w", *input.Key, err)
		abortErr := abortUpload()
		if abortErr != nil {
			err = fmt.Errorf("%w: can't abort upload: %w", err, abortErr)
		}
		return err
	}

	return nil
}
