package externalconsumerprobe_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/internal/externalconsumerprobe"
	externalconsumer "github.com/assurrussa/gouploads/reference/externalconsumer"
)

func TestConfigValidateRequiresVersionOrLocalPath(t *testing.T) {
	err := externalconsumerprobe.Config{
		ProbeModule: "example.com/gouploadsprobe",
		ModulePath:  "github.com/assurrussa/gouploads",
	}.Validate()

	require.EqualError(t, err, "external consumer probe: version or local path is required")
}

func TestConfigValidateRejectsVersionAndLocalPathTogether(t *testing.T) {
	err := externalconsumerprobe.Config{
		Version:   "v0.1.0",
		LocalPath: "/tmp/gouploads",
	}.Validate()

	require.EqualError(t, err, "external consumer probe: version and local path are mutually exclusive")
}

func TestBuildGoModIncludesReplaceForLocalPath(t *testing.T) {
	content, err := externalconsumerprobe.Config{
		ProbeModule: "example.com/gouploadsprobe",
		ModulePath:  "github.com/assurrussa/gouploads",
		LocalPath:   "/tmp/gouploads",
	}.BuildGoMod()
	require.NoError(t, err)

	require.Contains(t, content, "module example.com/gouploadsprobe")
	require.Contains(t, content, "require github.com/assurrussa/gouploads v0.0.0-local")
	require.Contains(t, content, "replace github.com/assurrussa/gouploads => /tmp/gouploads")
}

func TestBuildGoModUsesPublishedVersionWithoutReplace(t *testing.T) {
	content, err := externalconsumerprobe.Config{
		ProbeModule: "example.com/gouploadsprobe",
		ModulePath:  "github.com/assurrussa/gouploads",
		Version:     "v0.1.0",
	}.BuildGoMod()
	require.NoError(t, err)

	require.Contains(t, content, "require github.com/assurrussa/gouploads v0.1.0")
	require.NotContains(t, content, "replace github.com/assurrussa/gouploads")
}

func TestBuildProbeTestImportsSupportedPackagesAndUsesMigrationContract(t *testing.T) {
	content, err := externalconsumerprobe.Config{
		ProbeModule: "example.com/gouploadsprobe",
		ModulePath:  "github.com/assurrussa/gouploads",
		LocalPath:   "/tmp/gouploads",
	}.BuildProbeTest()
	require.NoError(t, err)

	require.Contains(t, content, "package probe")
	for _, pkg := range externalconsumer.SupportedPackages {
		require.Contains(t, content, `"`+pkg+`"`)
	}
	require.Contains(t, content, "host.MigrationsFS()")
	require.Contains(t, content, "host.MigrationFiles()")
	require.Contains(t, content, "host.NewFileRepo")
	require.Contains(t, content, "host.FileTypeAudio.ToID() != 7")
	require.Contains(t, content, "hosttest.SaveFileInput{}")
	require.Contains(t, content, "hosttest.NewListenResizeRequestMatcher")
}

func TestBuildProbeTestUsesConfiguredModulePath(t *testing.T) {
	content, err := externalconsumerprobe.Config{
		ProbeModule: "example.com/gouploadsprobe",
		ModulePath:  "example.com/fork/gouploads",
		LocalPath:   "/tmp/gouploads",
	}.BuildProbeTest()
	require.NoError(t, err)

	require.Contains(t, content, `"example.com/fork/gouploads/host"`)
	require.Contains(t, content, `"example.com/fork/gouploads/hosttest"`)
	require.NotContains(t, content, `"github.com/assurrussa/gouploads/host"`)
	require.NotContains(t, content, `"github.com/assurrussa/gouploads/hosttest"`)
}

func TestBuildIntegrationProbeTestUsesIntegrationTestSupport(t *testing.T) {
	content, err := externalconsumerprobe.Config{
		ProbeModule: "example.com/gouploadsprobe",
		ModulePath:  "github.com/assurrussa/gouploads",
		LocalPath:   "/tmp/gouploads",
	}.BuildIntegrationProbeTest()
	require.NoError(t, err)

	require.Contains(t, content, "//go:build integration")
	require.Contains(t, content, `"github.com/assurrussa/gouploads/hosttest"`)
	require.Contains(t, content, "hosttest.PrepareDB")
	require.Contains(t, content, "hosttest.CleanUp")
	require.Contains(t, content, "(*hosttest.DBHelper).CreateTable")
	require.Contains(t, content, "(*hosttest.DBHelper).TruncateTable")
	require.Contains(t, content, "(*hosttest.DBHelper).DropTable")
	require.Contains(t, content, "hosttest.WithDatabasePathFilesMigration")
}

func TestBuildIntegrationProbeTestUsesConfiguredModulePath(t *testing.T) {
	content, err := externalconsumerprobe.Config{
		ProbeModule: "example.com/gouploadsprobe",
		ModulePath:  "example.com/fork/gouploads",
		LocalPath:   "/tmp/gouploads",
	}.BuildIntegrationProbeTest()
	require.NoError(t, err)

	require.Contains(t, content, `"example.com/fork/gouploads/hosttest"`)
	require.NotContains(t, content, `"github.com/assurrussa/gouploads/hosttest"`)
}
