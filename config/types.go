package config

import (
	"github.com/assurrussa/goshared/pkg/tools"
)

type ParseSize string

func (p ParseSize) String() string {
	return string(p)
}

func (p ParseSize) Value() int {
	messageSize, err := tools.ParseSize(p.String())
	if err != nil {
		return 0
	}

	return messageSize
}
