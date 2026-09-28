package finalizeoriginal

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const JobName = "finalize_original_file"

// Payload contains no URLs or file paths. The worker loads the trusted source
// location from the locked file row, never from an HTTP callback.
type Payload struct {
	FileID int64 `json:"fileId"`
}

func MarshalPayload(payload Payload) (string, error) {
	if payload.FileID <= 0 {
		return "", errors.New("original finalization requires a positive file ID")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal original finalization: %w", err)
	}
	return string(data), nil
}

func UnmarshalPayload(data string) (Payload, error) {
	var payload Payload
	decoder := json.NewDecoder(strings.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return Payload{}, fmt.Errorf("decode original finalization: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Payload{}, errors.New("original finalization contains trailing JSON")
	}
	if payload.FileID <= 0 {
		return Payload{}, errors.New("original finalization requires a positive file ID")
	}
	return payload, nil
}
