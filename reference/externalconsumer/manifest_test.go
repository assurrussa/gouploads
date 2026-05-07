package externalconsumer

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSupportedPackagesMatchesBlankImports(t *testing.T) {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join(".", "imports.go"), nil, parser.ImportsOnly)
	require.NoError(t, err)

	var imported []string
	for _, spec := range file.Imports {
		require.NotNil(t, spec)
		require.NotNil(t, spec.Name)
		require.Equal(t, "_", spec.Name.Name)

		path, err := strconv.Unquote(spec.Path.Value)
		require.NoError(t, err)
		imported = append(imported, path)
	}

	slices.Sort(imported)

	supported := slices.Collect(slices.Values(SupportedPackages))
	slices.Sort(supported)

	require.Equal(t, supported, imported)
	require.Equal(t, len(SupportedPackages), SupportedPackageCount)
}

func TestImportsFileContainsOnlyBlankImports(t *testing.T) {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join(".", "imports.go"), nil, 0)
	require.NoError(t, err)

	for _, decl := range file.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.IMPORT {
			continue
		}

		for _, spec := range genDecl.Specs {
			importSpec, ok := spec.(*ast.ImportSpec)
			require.True(t, ok)
			require.NotNil(t, importSpec.Name)
			require.Equal(t, "_", importSpec.Name.Name)
		}
	}
}

func TestSupportedPackagesHaveReleaseSurfaceCategory(t *testing.T) {
	t.Helper()

	supported := slices.Collect(slices.Values(SupportedPackages))
	slices.Sort(supported)

	categorized := make([]string, 0, len(EmbeddingPackages))
	categorized = append(categorized, EmbeddingPackages[:]...)
	slices.Sort(categorized)

	require.NotEmpty(t, EmbeddingPackages)
	require.Equal(t, supported, categorized)
}
