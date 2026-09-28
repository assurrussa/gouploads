package bytesize

import (
	"errors"
)

const (
	Byte = 1
	KB   = 1 << 10
	MB   = 1 << 20
	GB   = 1 << 30
)

var ErrIncorrectSize = errors.New("incorrect size")

// Parse parses a human-readable size string (e.g. "10MB", "20KB", "1GB", "100B") into bytes.
func Parse(text string) (int, error) {
	if len(text) == 0 || text[0] < '0' || text[0] > '9' {
		return 0, ErrIncorrectSize
	}

	idx := 0
	size := 0
	for idx < len(text) && text[idx] >= '0' && text[idx] <= '9' {
		number := int(text[idx] - '0')
		if size > (int(^uint(0)>>1)-number)/10 {
			return 0, ErrIncorrectSize
		}
		size = size*10 + number
		idx++
	}

	parameter := text[idx:]
	var multiplier int
	switch parameter {
	case "GB", "Gb", "gb":
		multiplier = GB
	case "MB", "Mb", "mb":
		multiplier = MB
	case "KB", "Kb", "kb":
		multiplier = KB
	case "B", "b", "":
		multiplier = Byte
	default:
		return 0, ErrIncorrectSize
	}
	if size > int(^uint(0)>>1)/multiplier {
		return 0, ErrIncorrectSize
	}
	return size * multiplier, nil
}
