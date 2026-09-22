package config

import (
	"github.com/assurrussa/goshared/pkg/bytesize"
)

type ParseSize string

func (p ParseSize) String() string {
	return string(p)
}

func (p ParseSize) Value() int {
	messageSize, err := bytesize.Parse(p.String())
	if err != nil {
		return 0
	}

	return messageSize
}
