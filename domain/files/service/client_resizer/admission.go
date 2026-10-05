package clientresizer

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// AdmissionError preserves the HTTP outcome without retaining a response body
// that might contain signed source URLs or credentials.
type AdmissionError struct {
	StatusCode int
	RetryAfter time.Duration
}

func (e *AdmissionError) Error() string {
	return fmt.Sprintf("send resize request: unexpected status %d", e.StatusCode)
}

// Retryable distinguishes transient admission failures from a conflicting key
// or invalid request. A retry must retain the logical request's original key.
func (e *AdmissionError) Retryable() bool {
	return e.StatusCode == http.StatusRequestTimeout || e.StatusCode == http.StatusTooManyRequests || e.StatusCode >= 500
}

func retryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseInt(value, 10, 32); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if deadline, err := http.ParseTime(value); err == nil && deadline.After(now) {
		return deadline.Sub(now)
	}
	return 0
}
