package tusupload

const (
	Version       = "1.0.0"
	ContentType   = "application/offset+octet-stream"
	Extension     = "creation"
	SniffLen      = 512
	ExposeHeaders = "Tus-Resumable, Tus-Version, Tus-Extension, Tus-Max-Size, Location, Upload-Offset, Upload-Length"
)
