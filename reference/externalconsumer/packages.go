package externalconsumer

// EmbeddingPackages are the stable packages used to embed gouploads into a
// host application process.
var EmbeddingPackages = [...]string{
	"github.com/assurrussa/gouploads/host",
}

// TestSupportPackages are stable packages for external consumer tests and test
// helpers. Runtime host code should use EmbeddingPackages instead.
var TestSupportPackages = [...]string{
	"github.com/assurrussa/gouploads/hosttest",
}

var SupportedPackages = joinPackageGroups(
	EmbeddingPackages[:],
	TestSupportPackages[:],
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
