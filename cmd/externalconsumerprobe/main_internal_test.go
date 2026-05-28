package main

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProbeTestArgsWithoutTags(t *testing.T) {
	args, err := probeTestArgs(nil)

	require.NoError(t, err)
	require.Equal(t, []string{nameArgs, modArgs, pathFolderArgs, countArgs}, args)
	require.False(t, slices.Contains(args, "-tags"))
}

func TestProbeTestArgsWithTags(t *testing.T) {
	args, err := probeTestArgs([]string{integrationTag, "postgres"})

	require.NoError(t, err)
	require.Equal(t, []string{nameArgs, modArgs, pathFolderArgs, countArgs, "-tags", "integration,postgres"}, args)
}

func TestProbeTestArgsRejectsUnsafeTag(t *testing.T) {
	args, err := probeTestArgs([]string{integrationTag, "-race"})

	require.Nil(t, args)
	require.EqualError(t, err, "build tag must not start with '-'")
}

func TestModuleVersionArgBuildsGoListModuleRef(t *testing.T) {
	arg, err := moduleVersionArg(" github.com/assurrussa/gouploads ", " v0.8.0 ")

	require.NoError(t, err)
	require.Equal(t, "github.com/assurrussa/gouploads@v0.8.0", arg)
}

func TestModuleVersionArgRejectsUnsafeModulePath(t *testing.T) {
	arg, err := moduleVersionArg("-modfile=evil", "v0.8.0")

	require.Empty(t, arg)
	require.EqualError(t, err, "module path must not start with '-'")
}

func TestModuleVersionArgRejectsUnsafeVersion(t *testing.T) {
	arg, err := moduleVersionArg("github.com/assurrussa/gouploads", "v0.8.0\nreplace example.com/a => /tmp/a")

	require.Empty(t, arg)
	require.EqualError(t, err, "version must contain only visible ASCII characters")
}

func TestCommandEnvAddsGoModCache(t *testing.T) {
	env := commandEnv("/tmp/gomodcache")

	require.Contains(t, env, "GOMODCACHE=/tmp/gomodcache")
}
