package shared

type FileModerateStatus int16

const (
	FileModerateStatusDefault FileModerateStatus = 0
)

var FileModerateStatuses = map[FileModerateStatus]string{
	FileModerateStatusDefault: "Default",
}

func (s FileModerateStatus) Int() int {
	return int(s)
}

func (s FileModerateStatus) String() string {
	val := FileModerateStatuses[s]
	return val
}

func (s FileModerateStatus) Validate() bool {
	_, ok := FileModerateStatuses[s]

	return ok
}
