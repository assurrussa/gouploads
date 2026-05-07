package host

import (
	"github.com/assurrussa/godi"

	uploadgodi "github.com/assurrussa/gouploads/di"
)

func BootstrapDependencies() godi.Dependencies {
	return uploadgodi.ModuleBootstrap()
}
