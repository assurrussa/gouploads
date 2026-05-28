package importpolicy

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const gouploadsModulePath = "github.com/assurrussa/gouploads"

type Config struct {
	RepoRoot                 string
	ConsumerRoots            []string
	SupportedPackages        []string
	SupportedRuntimePackages []string
	SupportedTestPackages    []string
}

type Report struct {
	UnsupportedImports []UnsupportedImport
}

type UnsupportedImport struct {
	File       string
	ImportPath string
}

func (r Report) OK() bool {
	return len(r.UnsupportedImports) == 0
}

func (r Report) Message() string {
	if len(r.UnsupportedImports) == 0 {
		return ""
	}

	lines := make([]string, 0, len(r.UnsupportedImports))
	for _, violation := range r.UnsupportedImports {
		lines = append(lines, fmt.Sprintf("%s -> %s", violation.File, violation.ImportPath))
	}

	return "Unsupported gouploads imports:\n" + strings.Join(lines, "\n")
}

func Check(cfg Config) (Report, error) {
	if cfg.RepoRoot == "" {
		cfg.RepoRoot = "."
	}

	runtimeSupported := supportedPackages(cfg.SupportedRuntimePackages, cfg.SupportedPackages)
	testSupported := supportedPackages(cfg.SupportedTestPackages, cfg.SupportedPackages)

	var violations []UnsupportedImport
	err := walkGoFiles(cfg.RepoRoot, cfg.ConsumerRoots, func(path string) error {
		relPath := rel(cfg.RepoRoot, path)
		if isIgnoredGoFile(relPath) {
			return nil
		}
		supported := runtimeSupported
		if isTestSupportGoFile(relPath) {
			supported = testSupported
		}

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}

		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}

			if !isGouploadsImport(importPath) {
				continue
			}
			if _, ok := supported[importPath]; ok {
				continue
			}

			violations = append(violations, UnsupportedImport{
				File:       relPath,
				ImportPath: importPath,
			})
		}

		return nil
	})
	if err != nil {
		return Report{}, err
	}

	slices.SortFunc(violations, func(a, b UnsupportedImport) int {
		if a.File == b.File {
			return strings.Compare(a.ImportPath, b.ImportPath)
		}
		return strings.Compare(a.File, b.File)
	})

	return Report{UnsupportedImports: violations}, nil
}

func isGouploadsImport(importPath string) bool {
	return importPath == gouploadsModulePath || strings.HasPrefix(importPath, gouploadsModulePath+"/")
}

func supportedPackages(primary []string, fallback []string) map[string]struct{} {
	packages := primary
	if len(packages) == 0 {
		packages = fallback
	}

	supported := make(map[string]struct{}, len(packages))
	for _, pkg := range packages {
		supported[pkg] = struct{}{}
	}

	return supported
}

func walkGoFiles(repoRoot string, roots []string, visit func(path string) error) error {
	var missingRoots []string
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}

		absoluteRoot := filepath.Join(repoRoot, filepath.FromSlash(root))
		if _, err := os.Stat(absoluteRoot); err != nil {
			if os.IsNotExist(err) {
				missingRoots = append(missingRoots, root)
				continue
			}
			return err
		}

		err := filepath.WalkDir(absoluteRoot, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			if d.IsDir() {
				if shouldSkipDir(d.Name()) {
					return filepath.SkipDir
				}
				return nil
			}

			if filepath.Ext(path) != ".go" {
				return nil
			}

			return visit(path)
		})
		if err != nil {
			return err
		}
	}

	if len(missingRoots) > 0 {
		slices.Sort(missingRoots)
		return fmt.Errorf("consumer roots not found under %s: %s", repoRoot, strings.Join(missingRoots, ", "))
	}

	return nil
}

func shouldSkipDir(name string) bool {
	switch name {
	case ".cache", ".git", ".go-cache", ".nuxt", "dist", "node_modules", "tmp", "vendor":
		return true
	default:
		return false
	}
}

func isIgnoredGoFile(path string) bool {
	normalized := filepath.ToSlash(path)
	return strings.HasSuffix(normalized, ".gen.go") ||
		strings.Contains(normalized, "/mocks/")
}

func isTestSupportGoFile(path string) bool {
	normalized := filepath.ToSlash(path)
	if strings.HasSuffix(normalized, "_test.go") {
		return true
	}

	for _, segment := range strings.Split(normalized, "/") {
		switch segment {
		case "tests", "testsupport":
			return true
		}
	}

	return false
}

func rel(root, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}

	return filepath.ToSlash(relative)
}
