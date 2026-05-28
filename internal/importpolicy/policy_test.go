package importpolicy_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/internal/importpolicy"
	"github.com/assurrussa/gouploads/reference/externalconsumer"
)

func TestCheckPassesCurrentSupportedImports(t *testing.T) {
	t.Helper()

	repoRoot := t.TempDir()
	writeGoFile(t, repoRoot, "backend/good.go", `
package backend

import _ "github.com/assurrussa/gouploads/host"
`)
	writeGoFile(t, repoRoot, "fixtures/second-go-host/good_test.go", `
package secondgohost

import (
	_ "github.com/assurrussa/gouploads/host"
	_ "github.com/assurrussa/gouploads/hosttest"
)
`)
	writeGoFile(t, repoRoot, "backend/tests/helper.go", `
package tests

import _ "github.com/assurrussa/gouploads/hosttest"
`)

	report, err := importpolicy.Check(importpolicy.Config{
		RepoRoot:                 repoRoot,
		ConsumerRoots:            []string{"backend", "fixtures/second-go-host"},
		SupportedRuntimePackages: externalconsumer.EmbeddingPackages[:],
		SupportedTestPackages:    externalconsumer.SupportedPackages,
	})
	require.NoError(t, err)
	require.True(t, report.OK(), report.Message())
}

func TestCheckRejectsRuntimeTestSupportImport(t *testing.T) {
	t.Helper()

	repoRoot := t.TempDir()
	writeGoFile(t, repoRoot, "backend/bad.go", `
package backend

import _ "github.com/assurrussa/gouploads/hosttest"
`)

	report, err := importpolicy.Check(importpolicy.Config{
		RepoRoot:                 repoRoot,
		ConsumerRoots:            []string{"backend"},
		SupportedRuntimePackages: externalconsumer.EmbeddingPackages[:],
		SupportedTestPackages:    externalconsumer.SupportedPackages,
	})
	require.NoError(t, err)
	require.False(t, report.OK())
	require.Equal(t, []importpolicy.UnsupportedImport{{
		File:       "backend/bad.go",
		ImportPath: "github.com/assurrussa/gouploads/hosttest",
	}}, report.UnsupportedImports)
}

func TestCheckRejectsUnsupportedBackendImport(t *testing.T) {
	t.Helper()

	repoRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "fixtures", "second-go-host"), 0o755))
	writeGoFile(t, repoRoot, "backend/bad.go", `
package backend

import _ "github.com/assurrussa/gouploads/domain/files/shared"
`)

	report, err := importpolicy.Check(importpolicy.Config{
		RepoRoot:                 repoRoot,
		ConsumerRoots:            []string{"backend", "fixtures/second-go-host"},
		SupportedRuntimePackages: externalconsumer.EmbeddingPackages[:],
		SupportedTestPackages:    externalconsumer.SupportedPackages,
	})
	require.NoError(t, err)
	require.False(t, report.OK())
	require.Equal(t, []importpolicy.UnsupportedImport{{
		File:       "backend/bad.go",
		ImportPath: "github.com/assurrussa/gouploads/domain/files/shared",
	}}, report.UnsupportedImports)
}

func TestCheckRejectsMissingConsumerRoots(t *testing.T) {
	t.Helper()

	repoRoot := t.TempDir()
	writeGoFile(t, repoRoot, "backend/good.go", `
package backend

import _ "github.com/assurrussa/gouploads/host"
`)

	_, err := importpolicy.Check(importpolicy.Config{
		RepoRoot:                 repoRoot,
		ConsumerRoots:            []string{"backend", "missing-consumer"},
		SupportedRuntimePackages: externalconsumer.EmbeddingPackages[:],
		SupportedTestPackages:    externalconsumer.SupportedPackages,
	})
	require.EqualError(t, err, "consumer roots not found under "+repoRoot+": missing-consumer")
}

func TestCheckRejectsDeepImportInUploadAdapter(t *testing.T) {
	t.Helper()

	repoRoot := t.TempDir()
	writeGoFile(t, repoRoot, "backend/internal/infrastructure/uploads/files.go", `
package uploads

import _ "github.com/assurrussa/gouploads/domain/files/shared"
`)

	report, err := importpolicy.Check(importpolicy.Config{
		RepoRoot:                 repoRoot,
		ConsumerRoots:            []string{"backend"},
		SupportedRuntimePackages: externalconsumer.EmbeddingPackages[:],
		SupportedTestPackages:    externalconsumer.SupportedPackages,
	})
	require.NoError(t, err)
	require.False(t, report.OK())
	require.Equal(t, []importpolicy.UnsupportedImport{{
		File:       "backend/internal/infrastructure/uploads/files.go",
		ImportPath: "github.com/assurrussa/gouploads/domain/files/shared",
	}}, report.UnsupportedImports)
}

func TestCheckRejectsUnsupportedTestImport(t *testing.T) {
	t.Helper()

	repoRoot := t.TempDir()
	writeGoFile(t, repoRoot, "backend/bad_test.go", `
package backend

import _ "github.com/assurrussa/gouploads/domain/files/tests"
`)

	report, err := importpolicy.Check(importpolicy.Config{
		RepoRoot:                 repoRoot,
		ConsumerRoots:            []string{"backend"},
		SupportedRuntimePackages: externalconsumer.EmbeddingPackages[:],
		SupportedTestPackages:    externalconsumer.SupportedPackages,
	})
	require.NoError(t, err)
	require.False(t, report.OK())
	require.Equal(t, []importpolicy.UnsupportedImport{{
		File:       "backend/bad_test.go",
		ImportPath: "github.com/assurrussa/gouploads/domain/files/tests",
	}}, report.UnsupportedImports)
}

func TestCheckIgnoresGeneratedAndMockImports(t *testing.T) {
	t.Helper()

	repoRoot := t.TempDir()
	writeGoFile(t, repoRoot, "backend/good_test.go", `
package backend

import _ "github.com/assurrussa/gouploads/hosttest"
`)
	writeGoFile(t, repoRoot, "backend/api.gen.go", `
package backend

import _ "github.com/assurrussa/gouploads/domain/files/model"
`)
	writeGoFile(t, repoRoot, "backend/mocks/file.go", `
package mocks

import _ "github.com/assurrussa/gouploads/domain/files/model"
`)

	report, err := importpolicy.Check(importpolicy.Config{
		RepoRoot:                 repoRoot,
		ConsumerRoots:            []string{"backend"},
		SupportedRuntimePackages: externalconsumer.EmbeddingPackages[:],
		SupportedTestPackages:    externalconsumer.SupportedPackages,
	})
	require.NoError(t, err)
	require.True(t, report.OK(), report.Message())
}

func TestCheckSkipsLocalCacheDirectories(t *testing.T) {
	t.Helper()

	repoRoot := t.TempDir()
	writeGoFile(t, repoRoot, "backend/.go-cache/pkg/mod/bad.go", `
package bad

import _ "github.com/assurrussa/gouploads/domain/files/model"
`)
	writeGoFile(t, repoRoot, "backend/.cache/bad.go", `
package bad

import _ "github.com/assurrussa/gouploads/domain/files/model"
`)

	report, err := importpolicy.Check(importpolicy.Config{
		RepoRoot:                 repoRoot,
		ConsumerRoots:            []string{"backend"},
		SupportedRuntimePackages: externalconsumer.EmbeddingPackages[:],
		SupportedTestPackages:    externalconsumer.SupportedPackages,
	})
	require.NoError(t, err)
	require.True(t, report.OK(), report.Message())
}

func writeGoFile(t *testing.T, root, name, content string) {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o600))
}
