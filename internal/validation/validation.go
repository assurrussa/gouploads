package validation

import (
	"fmt"

	"github.com/go-playground/validator/v10"
	optsGenValidator "github.com/kazhuravlev/options-gen/pkg/validator"

	"github.com/assurrussa/gouploads/internal/bytesize"
)

var Validator = validator.New()

//nolint:gochecknoinits // It`s need
func init() {
	MustRegisterValidation("parse-size", validateParseSize)
	optsGenValidator.Set(Validator)
}

func MustRegisterValidation(validatorName string, fn validator.Func) {
	err := Validator.RegisterValidation(validatorName, fn)
	if err != nil {
		panic(fmt.Sprintf("validator register %q: %v", validatorName, err))
	}
}

// validateParseSize implements validator.Func.
func validateParseSize(fl validator.FieldLevel) bool {
	count, err := bytesize.Parse(fl.Field().String())
	if count <= 0 {
		return false
	}

	return err == nil
}
