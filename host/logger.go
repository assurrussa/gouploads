package host

import (
	"log/slog"

	logger "github.com/assurrussa/gologger"
)

// Logger is the logging interface used across gouploads.
// *gologger.Log satisfies this interface directly.
type Logger = logger.Logger

// NewSlogLogger adapts a standard library *slog.Logger into host.Logger.
// If sl is nil, a discard logger is returned.
func NewSlogLogger(sl *slog.Logger) Logger {
	if sl == nil {
		return logger.Discard()
	}
	log, err := logger.New(logger.Config{}, logger.WithHandler(sl.Handler()))
	if err != nil {
		return logger.Discard()
	}
	return log
}
