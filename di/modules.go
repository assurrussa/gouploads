package di

import (
	sharedgodi "github.com/assurrussa/godi"
)

// ModuleBootstrap registers helpers to assemble bootstrap dependencies and server.
func ModuleBootstrap() sharedgodi.Dependencies {
	return sharedgodi.CollectDependencies(
		sharedgodi.NewDependency(provideFileRepo, sharedgodi.WithKey(KeyFilesRepo)),
		sharedgodi.NewDependency(provideTaskUploader, sharedgodi.WithKey(KeyFilesTaskUploader)),
		sharedgodi.NewDependency(provideFileLoaderFileRepo, sharedgodi.WithKey(KeyFilesLoaderFileRepo)),
		sharedgodi.NewDependency(provideFileLoader, sharedgodi.WithKey(KeyFilesLoader)),
		sharedgodi.NewDependency(provideTusStore, sharedgodi.WithKey(KeyFilesTusStore)),
		sharedgodi.NewDependency(provideFileStorage, sharedgodi.WithKey(KeyFilesStorage)),
		sharedgodi.NewDependency(provideSourceURLResolver, sharedgodi.WithKey(KeyFilesSourceURLResolver)),
		sharedgodi.NewDependency(provideResizerSettingsCache, sharedgodi.WithKey(KeyFilesResizerSettings)),
		sharedgodi.NewDependency(provideClientResizer, sharedgodi.WithKey(KeyFilesClientResizer)),
		sharedgodi.NewDependency(provideConfiguredUploadService, sharedgodi.WithKey(KeyFilesUploadService)),
		sharedgodi.NewDependency(provideEventFileAfterProcess, sharedgodi.WithKey(KeyFilesEventAfterProcess)),
		sharedgodi.NewDependency(provideUseCaseDeleteFile, sharedgodi.WithKey(KeyFilesUseCaseDelete)),
		sharedgodi.NewDependency(provideUseCaseSendResizeFile, sharedgodi.WithKey(KeyFilesUseCaseSendResize)),
		sharedgodi.NewDependency(provideUseCaseListenResizeFile, sharedgodi.WithKey(KeyFilesUseCaseListenResize)),
		sharedgodi.NewDependency(provideUseCaseUploadFile, sharedgodi.WithKey(KeyFilesUseCaseUpload)),
		sharedgodi.NewDependency(provideUseCaseUploadRawFile, sharedgodi.WithKey(KeyFilesUseCaseUploadRaw)),
		sharedgodi.NewDependency(provideUseCaseFinalizeOriginal, sharedgodi.WithKey(KeyFilesUseCaseFinalizeOriginal)),
	)
}
