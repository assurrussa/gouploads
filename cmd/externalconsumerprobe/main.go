package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	logger "github.com/assurrussa/gologger"

	externalconsumerprobe "github.com/assurrussa/gouploads/internal/externalconsumerprobe"
)

const (
	nameArgs       = "test"
	modArgs        = "-mod=mod"
	pathFolderArgs = "./..."
	countArgs      = "-count=1"
	integrationTag = "integration"
)

func main() {
	log := logger.Default()
	if err := run(context.Background(), os.Args[1:], log); err != nil {
		log.Error("main run function", logger.Error(err))
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, log logger.Logger) error {
	fs := flag.NewFlagSet("externalconsumerprobe", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	modulePath := fs.String("module", externalconsumerprobe.DefaultModulePath, "target Go module path")
	version := fs.String("version", "", "published target version to resolve and require")
	localPath := fs.String("local-path", "", "local checkout path for replace-based probe")
	goModCache := fs.String("go-mod-cache", "", "optional GOMODCACHE path for the probe commands")
	keepWorkdir := fs.Bool("keep-workdir", false, "keep the generated temporary probe module on disk")
	timeout := fs.Duration("timeout", 2*time.Minute, "timeout for each go command")

	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg := externalconsumerprobe.Config{
		ModulePath: *modulePath,
		Version:    *version,
		LocalPath:  *localPath,
	}
	if err := cfg.Validate(); err != nil {
		return err
	}

	if cfg.Version != "" {
		if err := goListModule(ctx, cfg.ModulePath, cfg.Version, *goModCache, *timeout); err != nil {
			return err
		}
	}

	workdir, err := os.MkdirTemp("", "gouploads-externalconsumerprobe-*")
	if err != nil {
		return fmt.Errorf("create probe workdir: %w", err)
	}
	if !*keepWorkdir {
		defer func() {
			_ = os.RemoveAll(workdir)
		}()
	}

	goMod, err := cfg.BuildGoMod()
	if err != nil {
		return err
	}
	testFile, err := cfg.BuildProbeTest()
	if err != nil {
		return err
	}
	integrationTestFile, err := cfg.BuildIntegrationProbeTest()
	if err != nil {
		return err
	}

	if err := os.WriteFile(filepath.Join(workdir, "go.mod"), []byte(goMod), 0o600); err != nil {
		return fmt.Errorf("write probe go.mod: %w", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "externalconsumer_probe_test.go"), []byte(testFile), 0o600); err != nil {
		return fmt.Errorf("write probe test: %w", err)
	}
	if err := os.WriteFile(
		filepath.Join(workdir, "externalconsumer_integration_probe_test.go"),
		[]byte(integrationTestFile),
		0o600,
	); err != nil {
		return fmt.Errorf("write integration probe test: %w", err)
	}

	if err := goTestProbe(ctx, workdir, nil, *goModCache, *timeout); err != nil {
		if *keepWorkdir {
			return fmt.Errorf("%w (workdir preserved at %s)", err, workdir)
		}
		return fmt.Errorf("%w (rerun with --keep-workdir to inspect generated probe module)", err)
	}
	if err := goTestProbe(ctx, workdir, []string{integrationTag}, *goModCache, *timeout); err != nil {
		if *keepWorkdir {
			return fmt.Errorf("%w (workdir preserved at %s)", err, workdir)
		}
		return fmt.Errorf("%w (rerun with --keep-workdir to inspect generated probe module)", err)
	}

	if *keepWorkdir {
		log.InfoContext(ctx, "keeping workdir: "+workdir)
	}

	return nil
}

func goListModule(ctx context.Context, modulePath string, version string, goModCache string, timeout time.Duration) error {
	moduleRef, err := moduleVersionArg(modulePath, version)
	if err != nil {
		return err
	}

	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(commandCtx, "go", "list", "-m", "-json", moduleRef)
	cmd.Env = commandEnv(goModCache)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		if errors.Is(commandCtx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("resolve published module %s@%s: timeout after %s", modulePath, version, timeout)
		}
		return fmt.Errorf("resolve published module %s@%s: %w", modulePath, version, err)
	}

	return nil
}

func goTestProbe(ctx context.Context, workdir string, tags []string, goModCache string, timeout time.Duration) error {
	args, err := probeTestArgs(tags)
	if err != nil {
		return err
	}

	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(commandCtx, "go", args...)
	cmd.Dir = workdir
	cmd.Env = commandEnv(goModCache)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		if errors.Is(commandCtx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("run external consumer probe: timeout after %s", timeout)
		}
		return fmt.Errorf("run external consumer probe: %w", err)
	}

	return nil
}

func probeTestArgs(tags []string) ([]string, error) {
	args := []string{nameArgs, modArgs, pathFolderArgs, countArgs}
	if len(tags) > 0 {
		tagArg, err := buildTagsArg(tags)
		if err != nil {
			return nil, err
		}
		args = append(args, "-tags", tagArg)
	}

	return args, nil
}

func moduleVersionArg(modulePath string, version string) (string, error) {
	cleanModulePath := strings.TrimSpace(modulePath)
	cleanVersion := strings.TrimSpace(version)

	if err := validateGoCommandArg("module path", cleanModulePath); err != nil {
		return "", err
	}
	if strings.Contains(cleanModulePath, "@") {
		return "", errors.New("module path must not contain @")
	}
	if err := validateGoCommandArg("version", cleanVersion); err != nil {
		return "", err
	}
	if strings.Contains(cleanVersion, "@") {
		return "", errors.New("version must not contain @")
	}

	return cleanModulePath + "@" + cleanVersion, nil
}

func buildTagsArg(tags []string) (string, error) {
	for _, tag := range tags {
		if err := validateGoCommandArg("build tag", tag); err != nil {
			return "", err
		}
		if strings.Contains(tag, ",") {
			return "", errors.New("build tag must not contain comma")
		}
	}

	return strings.Join(tags, ","), nil
}

func validateGoCommandArg(name string, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", name)
	}
	if strings.HasPrefix(value, "-") {
		return fmt.Errorf("%s must not start with '-'", name)
	}
	for _, char := range value {
		if char < '!' || char > '~' {
			return fmt.Errorf("%s must contain only visible ASCII characters", name)
		}
	}

	return nil
}

func commandEnv(goModCache string) []string {
	env := os.Environ()
	if goModCache == "" {
		return env
	}

	return append(env, "GOMODCACHE="+goModCache)
}
