package cleanerfiles

import (
	"errors"

	"github.com/assurrussa/goshared/pkg/validator"
)

type Request struct {
	BatchSize  int `validate:"required"`
	Iterations int `validate:"required"`
	Minutes    int
}

func (r Request) Validate() error {
	if r.BatchSize < 100 ||
		r.BatchSize > 5000 ||
		r.Iterations < 1 {
		return errors.New("invalid batch size")
	}

	return validator.Validator.Struct(r)
}

type Response struct {
	Total int64
}
