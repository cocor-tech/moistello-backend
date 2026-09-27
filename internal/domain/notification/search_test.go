package notification

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestBuildSearchConditions(t *testing.T) {
	uid := uuid.New()

	where, args := buildSearchConditions(uid, SearchFilter{})
	assert.Equal(t, "WHERE user_id = $1 AND is_archived = false", where)
	assert.Equal(t, []any{uid}, args)

	where, args = buildSearchConditions(uid, SearchFilter{
		Query:           " 50%_off ",
		Type:            TypeContributionDue,
		UnreadOnly:      true,
		IncludeArchived: true,
	})
	assert.Equal(t, "WHERE user_id = $1 AND is_read = false AND type = $2 AND (title ILIKE $3 OR body ILIKE $3)", where)
	assert.Equal(t, []any{uid, "contribution.due", `%50\%\_off%`}, args)
}
