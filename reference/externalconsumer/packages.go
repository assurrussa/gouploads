package externalconsumer

// EmbeddingPackages are the stable packages used to embed gouploads into a
// host application process.
var EmbeddingPackages = [...]string{
	"github.com/assurrussa/gouploads/host",
}

var SupportedPackages = joinPackageGroups(
	EmbeddingPackages[:],
)

var SupportedPackageCount = len(SupportedPackages)

func joinPackageGroups(groups ...[]string) []string {
	var count int
	for _, group := range groups {
		count += len(group)
	}

	packages := make([]string, 0, count)
	for _, group := range groups {
		packages = append(packages, group...)
	}

	return packages
}
