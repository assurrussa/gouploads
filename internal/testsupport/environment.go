package testsupport

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"

	"github.com/joho/godotenv"
)

func FindBasePath() (string, error) {
	basePath := os.Getenv("BASE_PATH")
	if basePath != "" {
		return basePath, nil
	}

	currentDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}

	for {
		goModPath := filepath.Join(currentDir, "go.mod")
		if _, err := os.Stat(goModPath); err == nil {
			return currentDir, nil
		}

		parentDir := filepath.Dir(currentDir)
		if parentDir == currentDir {
			return "", errors.New("go.mod not found")
		}

		currentDir = parentDir
	}
}

// FindFileDir searches upward from the caller's directory.
func FindFileDir(name, caller string) string {
	for dir := filepath.Dir(caller); ; dir = filepath.Dir(dir) {
		candidate := filepath.Join(dir, name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		if filepath.Dir(dir) == dir {
			return ""
		}
	}
}

func CallerCurrentFile() string {
	_, file, _, _ := runtime.Caller(1) //nolint:dogsled // Only the caller path is needed.
	return file
}

// LoadEnvironment preserves existing process values and optional override behavior.
func LoadEnvironment(caller string) {
	override := os.Getenv("ENV_OVERRIDE") == "1"
	if err := godotenv.Load(FindFileDir(".env", caller)); err != nil {
		slog.Default().Warn("not found .env file", slog.Any("error", err))
		return
	}
	if override {
		if err := godotenv.Overload(FindFileDir(".env.override", caller)); err != nil {
			slog.Default().Warn("not found .env.override file", slog.Any("error", err))
		}
	}
}
