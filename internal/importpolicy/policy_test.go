package importpolicy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/assurrussa/gouploads/reference/externalconsumer"
	"github.com/stretchr/testify/require"
)

func TestCheckPassesCurrentSupportedImports(t *testing.T) {
	t.Helper()

	repoRoot := t.TempDir()
	writeGoFile(t, repoRoot, "backend/good.go", `
package backend

import _ "github.com/assurrussa/gouploads/host"
`)
	writeGoFile(t, repoRoot, "fixtures/second-go-host/good.go", `
package secondgohost

import _ "github.com/assurrussa/gouploads/host"
`)

	report, err := Check(Config{
		RepoRoot:          repoRoot,
		ConsumerRoots:     []string{"backend", "fixtures/second-go-host"},
		SupportedPackages: externalconsumer.SupportedPackages,
	})
	require.NoError(t, err)
	require.True(t, report.OK(), report.Error())
}

func TestCheckRejectsUnsupportedBackendImport(t *testing.T) {
	t.Helper()

	repoRoot := t.TempDir()
	writeGoFile(t, repoRoot, "backend/bad.go", `
package backend

import _ "github.com/assurrussa/gouploads/domain/files/shared"
`)

	report, err := Check(Config{
		RepoRoot:          repoRoot,
		ConsumerRoots:     []string{"backend", "fixtures/second-go-host"},
		SupportedPackages: externalconsumer.SupportedPackages,
	})
	require.NoError(t, err)
	require.False(t, report.OK())
	require.Equal(t, []UnsupportedImport{{
		File:       "backend/bad.go",
		ImportPath: "github.com/assurrussa/gouploads/domain/files/shared",
	}}, report.UnsupportedImports)
}

func TestCheckAllowsTransitionalDeepImportDirectory(t *testing.T) {
	t.Helper()

	repoRoot := t.TempDir()
	writeGoFile(t, repoRoot, "backend/internal/infrastructure/uploads/files.go", `
package uploads

import _ "github.com/assurrussa/gouploads/domain/files/shared"
`)
	writeGoFile(t, repoRoot, "backend/internal/app/bad.go", `
package app

import _ "github.com/assurrussa/gouploads/di"
`)

	report, err := Check(Config{
		RepoRoot:              repoRoot,
		ConsumerRoots:         []string{"backend"},
		SupportedPackages:     externalconsumer.SupportedPackages,
		AllowedDeepImportDirs: []string{"backend/internal/infrastructure/uploads"},
	})
	require.NoError(t, err)
	require.False(t, report.OK())
	require.Equal(t, []UnsupportedImport{{
		File:       "backend/internal/app/bad.go",
		ImportPath: "github.com/assurrussa/gouploads/di",
	}}, report.UnsupportedImports)
}

func TestCheckIgnoresTestGeneratedAndMockImports(t *testing.T) {
	t.Helper()

	repoRoot := t.TempDir()
	writeGoFile(t, repoRoot, "backend/bad_test.go", `
package backend

import _ "github.com/assurrussa/gouploads/domain/files/tests"
`)
	writeGoFile(t, repoRoot, "backend/api.gen.go", `
package backend

import _ "github.com/assurrussa/gouploads/domain/files/model"
`)
	writeGoFile(t, repoRoot, "backend/mocks/file.go", `
package mocks

import _ "github.com/assurrussa/gouploads/domain/files/model"
`)

	report, err := Check(Config{
		RepoRoot:          repoRoot,
		ConsumerRoots:     []string{"backend"},
		SupportedPackages: externalconsumer.SupportedPackages,
	})
	require.NoError(t, err)
	require.True(t, report.OK(), report.Error())
}

func writeGoFile(t *testing.T, root, name, content string) {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o644))
}
