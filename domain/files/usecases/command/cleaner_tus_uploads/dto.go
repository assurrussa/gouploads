package cleanertusuploads

import (
	"errors"
	"time"
)

type Request struct {
	Before time.Time
}

func (r Request) Validate() error {
	if r.Before.IsZero() {
		return errors.New("before is required")
	}
	return nil
}

type Response struct {
	Total int64
}
