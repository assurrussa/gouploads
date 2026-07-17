//nolint:testpackage // validates the internal value contract directly
package shared

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFileObjectTypeValidateAcceptsSafeHostOwnedTypes(t *testing.T) {
	t.Parallel()

	for _, objectType := range []FileObjectType{
		ObjectTypeAdmin,
		ObjectTypeBonusLesson,
		ObjectTypeKnowledgeBase,
		ObjectTypeExercise,
		"post",
		"author",
		"media_asset_2",
	} {
		require.NoError(t, objectType.Validate(), objectType)
	}
}

func TestFileObjectTypeValidateRejectsUnsafeTypes(t *testing.T) {
	t.Parallel()

	for _, objectType := range []FileObjectType{
		"",
		"Post",
		"2post",
		"_post",
		"post-author",
		"post/author",
		"../post",
		"post author",
		"автор",
		FileObjectType(strings.Repeat("a", 65)),
	} {
		require.Error(t, objectType.Validate(), objectType)
	}
}
