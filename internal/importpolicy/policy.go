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
	RepoRoot              string
	ConsumerRoots         []string
	SupportedPackages     []string
	AllowedDeepImportDirs []string
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

func (r Report) Error() string {
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

	supported := make(map[string]struct{}, len(cfg.SupportedPackages))
	for _, pkg := range cfg.SupportedPackages {
		supported[pkg] = struct{}{}
	}

	allowedDeepImportDirs := normalizeDirs(cfg.AllowedDeepImportDirs)

	var violations []UnsupportedImport
	err := walkGoFiles(cfg.RepoRoot, cfg.ConsumerRoots, func(path string) error {
		relPath := rel(cfg.RepoRoot, path)
		if isIgnoredGoFile(relPath) {
			return nil
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
			if isUnderAnyDir(relPath, allowedDeepImportDirs) {
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

func normalizeDirs(dirs []string) []string {
	out := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		dir = strings.Trim(filepath.ToSlash(strings.TrimSpace(dir)), "/")
		if dir == "" {
			continue
		}
		out = append(out, dir)
	}
	return out
}

func isUnderAnyDir(file string, dirs []string) bool {
	file = strings.Trim(filepath.ToSlash(file), "/")
	for _, dir := range dirs {
		if file == dir || strings.HasPrefix(file, dir+"/") {
			return true
		}
	}
	return false
}

func walkGoFiles(repoRoot string, roots []string, visit func(path string) error) error {
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}

		absoluteRoot := filepath.Join(repoRoot, filepath.FromSlash(root))
		if _, err := os.Stat(absoluteRoot); err != nil {
			if os.IsNotExist(err) {
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

	return nil
}

func shouldSkipDir(name string) bool {
	switch name {
	case ".git", ".nuxt", "dist", "node_modules", "tmp", "vendor":
		return true
	default:
		return false
	}
}

func isIgnoredGoFile(path string) bool {
	normalized := filepath.ToSlash(path)
	return strings.HasSuffix(normalized, "_test.go") ||
		strings.HasSuffix(normalized, ".gen.go") ||
		strings.Contains(normalized, "/mocks/")
}

func rel(root, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}

	return filepath.ToSlash(relative)
}
