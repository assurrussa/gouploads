package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/assurrussa/gouploads/internal/importpolicy"
	"github.com/assurrussa/gouploads/reference/externalconsumer"
)

func main() {
	var repoRoot string
	var consumers csvFlag
	var allowedDeepImportDirs csvFlag

	flag.StringVar(&repoRoot, "repo-root", "..", "repository root to scan")
	flag.Var(&consumers, "consumers", "comma-separated host consumer roots")
	flag.Var(&allowedDeepImportDirs, "allow-deep-dir", "comma-separated transitional directories allowed to import gouploads internals")
	flag.Parse()

	if len(consumers) == 0 {
		consumers = csvFlag{"backend", "fixtures/second-go-host"}
	}

	report, err := importpolicy.Check(importpolicy.Config{
		RepoRoot:              repoRoot,
		ConsumerRoots:         consumers,
		SupportedPackages:     externalconsumer.SupportedPackages,
		AllowedDeepImportDirs: allowedDeepImportDirs,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "gouploads import policy check failed: %v\n", err)
		os.Exit(1)
	}
	if !report.OK() {
		fmt.Fprintln(os.Stderr, report.Error())
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Only packages listed in gouploads/reference/externalconsumer are stable for host consumers.")
		os.Exit(1)
	}
}

type csvFlag []string

func (f *csvFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *csvFlag) Set(value string) error {
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		*f = append(*f, item)
	}
	return nil
}
