package externalconsumerprobe

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	externalconsumer "github.com/assurrussa/gouploads/reference/externalconsumer"
)

const (
	DefaultProbeModule = "example.com/gouploadsprobe"
	DefaultModulePath  = "github.com/assurrussa/gouploads"
	LocalModuleVersion = "v0.0.0-local"
)

type Config struct {
	ProbeModule string
	ModulePath  string
	Version     string
	LocalPath   string
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.Version) == "" && strings.TrimSpace(c.LocalPath) == "" {
		return errors.New("external consumer probe: version or local path is required")
	}
	if strings.TrimSpace(c.Version) != "" && strings.TrimSpace(c.LocalPath) != "" {
		return errors.New("external consumer probe: version and local path are mutually exclusive")
	}

	return nil
}

func (c Config) BuildGoMod() (string, error) {
	cfg := c.normalized()
	if err := cfg.Validate(); err != nil {
		return "", err
	}

	var builder strings.Builder
	_, _ = builder.WriteString("module ")
	_, _ = builder.WriteString(cfg.ProbeModule)
	_, _ = builder.WriteString("\n\ngo 1.26\n\nrequire ")
	_, _ = builder.WriteString(cfg.ModulePath)
	_, _ = builder.WriteString(" ")
	_, _ = builder.WriteString(cfg.targetVersion())
	_, _ = builder.WriteString("\n")

	if cfg.LocalPath != "" {
		_, _ = builder.WriteString("\nreplace ")
		_, _ = builder.WriteString(cfg.ModulePath)
		_, _ = builder.WriteString(" => ")
		_, _ = builder.WriteString(filepath.Clean(cfg.LocalPath))
		_, _ = builder.WriteString("\n")
	}

	return builder.String(), nil
}

func (c Config) BuildProbeTest() (string, error) {
	cfg := c.normalized()
	if err := cfg.Validate(); err != nil {
		return "", err
	}

	var builder strings.Builder
	_, _ = builder.WriteString("package probe\n\n")
	_, _ = builder.WriteString("import (\n")
	_, _ = builder.WriteString("\t\"io/fs\"\n")
	_, _ = builder.WriteString("\t\"testing\"\n")
	for _, pkg := range externalconsumer.SupportedPackages {
		importPath := cfg.packagePath(pkg)
		switch pkg {
		case "github.com/assurrussa/gouploads/host":
			_, _ = fmt.Fprintf(&builder, "\thost %q\n", importPath)
		case "github.com/assurrussa/gouploads/hosttest":
			_, _ = fmt.Fprintf(&builder, "\thosttest %q\n", importPath)
		default:
			_, _ = fmt.Fprintf(&builder, "\t_ %q\n", importPath)
		}
	}
	_, _ = builder.WriteString(")\n\n")
	_, _ = builder.WriteString("func TestSupportedPackagesCompile(t *testing.T) {\n")
	_, _ = builder.WriteString("\tmigrationFS, err := host.MigrationsFS()\n")
	_, _ = builder.WriteString("\tif err != nil { t.Fatal(err) }\n")
	_, _ = builder.WriteString("\tfiles, err := host.MigrationFiles()\n")
	_, _ = builder.WriteString("\tif err != nil { t.Fatal(err) }\n")
	_, _ = builder.WriteString("\tif len(files) == 0 { t.Fatal(\"no gouploads migrations exposed\") }\n")
	_, _ = builder.WriteString("\tif _, err := fs.ReadFile(migrationFS, files[0]); err != nil { t.Fatal(err) }\n")
	_, _ = builder.WriteString("\t_ = host.StorageConfig{}\n")
	_, _ = builder.WriteString("\t_ = host.OutboxJobDeps{}\n")
	_, _ = builder.WriteString("\t_ = host.NewFileRepo\n")
	_, _ = builder.WriteString("\t_ = host.MustFileRepo\n")
	_, _ = builder.WriteString("\t_ = hosttest.SaveFileInput{}\n")
	_, _ = builder.WriteString("\t_ = hosttest.NewListenResizeRequestMatcher\n")
	_, _ = builder.WriteString("}\n")

	return builder.String(), nil
}

func (c Config) BuildIntegrationProbeTest() (string, error) {
	cfg := c.normalized()
	if err := cfg.Validate(); err != nil {
		return "", err
	}

	var builder strings.Builder
	_, _ = builder.WriteString("//go:build integration\n\n")
	_, _ = builder.WriteString("package probe\n\n")
	_, _ = builder.WriteString("import (\n")
	_, _ = builder.WriteString("\t\"context\"\n")
	_, _ = builder.WriteString("\t\"testing\"\n")
	_, _ = fmt.Fprintf(&builder, "\thosttest %q\n", cfg.packagePath("github.com/assurrussa/gouploads/hosttest"))
	_, _ = builder.WriteString(")\n\n")
	_, _ = builder.WriteString("func TestIntegrationTestSupportCompiles(t *testing.T) {\n")
	_, _ = builder.WriteString("\tvar _ func(context.Context, *testing.T, string, ...hosttest.OptionDatabase) (*hosttest.PgsqlClient, *hosttest.DBHelper, hosttest.CleanUp) = hosttest.PrepareDB\n") //nolint:lll // need
	_, _ = builder.WriteString("\tvar _ func(*hosttest.DBHelper, context.Context, string, string) = (*hosttest.DBHelper).CreateTable\n")                                                             //nolint:lll // need
	_, _ = builder.WriteString("\tvar _ func(*hosttest.DBHelper, context.Context, string) = (*hosttest.DBHelper).TruncateTable\n")                                                                   //nolint:lll // need
	_, _ = builder.WriteString("\tvar _ func(*hosttest.DBHelper, context.Context, string) = (*hosttest.DBHelper).DropTable\n")                                                                       //nolint:lll // need
	_, _ = builder.WriteString("\t_ = hosttest.WithDatabasePathFilesMigration\n")
	_, _ = builder.WriteString("\t_ = hosttest.WithDatabaseFixedName\n")
	_, _ = builder.WriteString("\t_ = hosttest.WithDatabaseVerbose\n")
	_, _ = builder.WriteString("\t_ = hosttest.WithDatabaseLog\n")
	_, _ = builder.WriteString("}\n")

	return builder.String(), nil
}

func (c Config) normalized() Config {
	cfg := c
	cfg.ProbeModule = strings.TrimSpace(cfg.ProbeModule)
	cfg.ModulePath = strings.TrimSpace(cfg.ModulePath)
	cfg.Version = strings.TrimSpace(cfg.Version)
	cfg.LocalPath = strings.TrimSpace(cfg.LocalPath)

	if cfg.ProbeModule == "" {
		cfg.ProbeModule = DefaultProbeModule
	}
	if cfg.ModulePath == "" {
		cfg.ModulePath = DefaultModulePath
	}

	return cfg
}

func (c Config) targetVersion() string {
	if strings.TrimSpace(c.Version) != "" {
		return strings.TrimSpace(c.Version)
	}

	return LocalModuleVersion
}

func (c Config) packagePath(pkg string) string {
	modulePath := strings.TrimSpace(c.ModulePath)
	if modulePath == "" {
		modulePath = DefaultModulePath
	}

	if pkg == DefaultModulePath {
		return modulePath
	}
	if strings.HasPrefix(pkg, DefaultModulePath+"/") {
		return modulePath + strings.TrimPrefix(pkg, DefaultModulePath)
	}

	return pkg
}
