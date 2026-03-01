package testsmatcher

import (
	"fmt"

	"go.uber.org/mock/gomock"
)

var _ gomock.Matcher = &BaseMatcher[any]{}

type FnMockChecker[T any] func(expected T, got T, unequalFields []string) ([]string, bool)

// BaseMatcher is intended to be used only in tests.
type BaseMatcher[T any] struct {
	expected      T
	name          string
	unequalFields []string
	fnChecker     FnMockChecker[T]
}

func NewBaseMatcher[T any](name string, expected T, fnChecker FnMockChecker[T]) *BaseMatcher[T] {
	return &BaseMatcher[T]{
		expected:      expected,
		name:          name,
		unequalFields: make([]string, 0, 10),
		fnChecker:     fnChecker,
	}
}

func (m *BaseMatcher[T]) Matches(x any) bool {
	got, ok := x.(T)
	if !ok {
		return false
	}

	var resultCheck bool
	m.unequalFields, resultCheck = m.fnChecker(m.expected, got, m.unequalFields)
	return resultCheck
}

func (m *BaseMatcher[T]) String() string {
	return messageResultInError(m.unequalFields, m.expected, m.name)
}

func messageResultInError[T any](unequalFields []string, expected T, name string) string {
	return fmt.Sprintf("diff[%v]\n %v - %s", unequalFields, expected, name)
}
