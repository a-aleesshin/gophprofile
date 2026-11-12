//go:build integration

package postgres

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
	"github.com/a-aleesshin/gophprofile/profile/internal/usecase"
)

func TestViewByIDProjectsAReadyAvatar(t *testing.T) {
	t.Parallel()

	// Arrange
	avatars := NewAvatars(sharedDB)
	user := newUser(t)
	ready := newReadyAvatar(t, user)

	// Act
	view, err := avatars.ViewByID(t.Context(), ready.ID())

	// Assert
	require.NoError(t, err)
	assert.True(t, view.ID.Equal(ready.ID()))
	assert.Equal(t, user.String(), view.UserID.String())
	assert.Equal(t, ready.FileID().String(), view.FileID.String())
	assert.True(t, view.Status.Equal(domain.AvatarStatusReady))
	assert.Equal(t, "avatar.jpg", view.FileName)
	assert.Equal(t, int64(2048), view.SizeBytes)
	assert.Equal(t, int32(1920), view.Width)

	variant, found := view.VariantByName("100x100")
	require.True(t, found)
	assert.Equal(t, "jpg", variant.Ext)
	assert.Equal(t, int32(100), variant.Height)
}

func TestViewByIDProjectsAPendingAvatarWithoutVariants(t *testing.T) {
	t.Parallel()

	// Arrange
	avatars := NewAvatars(sharedDB)
	pending := newPendingAvatar(t, newUser(t))

	// Act
	view, err := avatars.ViewByID(t.Context(), pending.ID())

	// Assert
	require.NoError(t, err)
	assert.True(t, view.Status.Equal(domain.AvatarStatusPending))
	assert.True(t, view.FileID.IsZero())
	assert.Empty(t, view.Variants)
	assert.Zero(t, view.SizeBytes, "nullable columns must not become garbage zeroes elsewhere")
}

func TestViewByIDHidesADeletedAvatar(t *testing.T) {
	t.Parallel()

	// Arrange
	avatars := NewAvatars(sharedDB)
	deleted := newReadyAvatar(t, newUser(t))

	require.NoError(t, deleted.MarkDeleted(time.Now()))
	require.NoError(t, avatars.Save(t.Context(), &deleted))

	// Act
	_, err := avatars.ViewByID(t.Context(), deleted.ID())

	// Assert
	require.ErrorIs(t, err, domain.ErrAvatarNotFound,
		"the aggregate still exists for idempotent delete, the projection must not")
}

func TestViewsByUserOrderNewestFirstAndSkipDeleted(t *testing.T) {
	t.Parallel()

	// Arrange
	avatars := NewAvatars(sharedDB)
	user := newUser(t)
	base := time.Now().Truncate(time.Microsecond)

	oldest := newPendingAvatarAt(t, user, base.Add(-2*time.Hour))
	middle := newPendingAvatarAt(t, user, base.Add(-time.Hour))
	newest := newPendingAvatarAt(t, user, base)

	gone := newPendingAvatarAt(t, user, base.Add(-30*time.Minute))
	require.NoError(t, gone.MarkDeleted(time.Now()))
	require.NoError(t, avatars.Save(t.Context(), &gone))

	// Act
	views, err := avatars.ViewsByUser(t.Context(), user, usecase.Page{Limit: 10})

	// Assert
	require.NoError(t, err)
	require.Len(t, views, 3)
	assert.True(t, views[0].ID.Equal(newest.ID()))
	assert.True(t, views[1].ID.Equal(middle.ID()))
	assert.True(t, views[2].ID.Equal(oldest.ID()))
}

func TestViewsByUserPageWithoutSkippingATie(t *testing.T) {
	t.Parallel()

	// Arrange: three avatars share one created_at, which is what breaks a
	// cursor that carries only a timestamp.
	avatars := NewAvatars(sharedDB)
	user := newUser(t)
	shared := time.Now().Truncate(time.Microsecond)

	for range 3 {
		newPendingAvatarAt(t, user, shared)
	}

	newPendingAvatarAt(t, user, shared.Add(-time.Minute))

	// Act
	first, err := avatars.ViewsByUser(t.Context(), user, usecase.Page{Limit: 2})
	require.NoError(t, err)
	require.Len(t, first, 2)

	second, err := avatars.ViewsByUser(t.Context(), user, usecase.Page{
		Limit:          2,
		AfterCreatedAt: first[1].CreatedAt,
		AfterID:        first[1].ID,
	})
	require.NoError(t, err)

	require.Len(t, second, 2)

	third, err := avatars.ViewsByUser(t.Context(), user, usecase.Page{
		Limit:          2,
		AfterCreatedAt: second[1].CreatedAt,
		AfterID:        second[1].ID,
	})
	require.NoError(t, err)
	assert.Empty(t, third, "four rows fit in two pages of two")

	// Assert
	seen := make(map[string]int, 4)
	for _, page := range [][]usecase.AvatarView{first, second, third} {
		for _, view := range page {
			seen[view.ID.String()]++
		}
	}

	assert.Len(t, seen, 4, "every row appears")

	for id, times := range seen {
		assert.Equal(t, 1, times, "%s appeared %d times", id, times)
	}
}

func TestViewsByUserOfAStrangerAreEmpty(t *testing.T) {
	t.Parallel()

	avatars := NewAvatars(sharedDB)
	newReadyAvatar(t, newUser(t))

	views, err := avatars.ViewsByUser(t.Context(), testUserID(t), usecase.Page{Limit: 10})

	require.NoError(t, err)
	assert.Empty(t, views)
}

func TestViewsByUserUseTheIndexAndDoNotSort(t *testing.T) {
	// Not parallel: the planner choice is only meaningful once the table is big
	// enough, and this test is what makes it big.
	user := newUser(t)

	_, err := sharedDB.Pool().Exec(t.Context(), `
		INSERT INTO avatars (id, version, user_id, upload_id, status, created_at, updated_at)
		SELECT gen_random_uuid(), 1, $1, gen_random_uuid(), 'pending',
		       now() - (step || ' seconds')::interval,
		       now()
		  FROM generate_series(1, 5000) AS step`, user.String())
	require.NoError(t, err)

	_, err = sharedDB.Pool().Exec(t.Context(), `ANALYZE avatars`)
	require.NoError(t, err)

	var plan string

	rows, err := sharedDB.Pool().Query(t.Context(), `
		EXPLAIN (ANALYZE, COSTS OFF)
		SELECT id FROM avatars
		 WHERE user_id = $1
		   AND deleted_at IS NULL
		   AND ($2::timestamptz IS NULL OR (created_at, id) < ($2, $3))
		 ORDER BY created_at DESC, id DESC
		 LIMIT 21`, user.String(), time.Now().Add(-time.Hour), uuid.Max)
	require.NoError(t, err)

	defer rows.Close()

	for rows.Next() {
		var line string

		require.NoError(t, rows.Scan(&line))

		plan += line + "\n"
	}

	require.NoError(t, rows.Err())

	t.Log("\n" + plan)

	assert.Contains(t, plan, "idx_avatars_user",
		"the partial index is the whole reason the gallery is cheap")
	assert.NotContains(t, plan, "Seq Scan on avatars")
	assert.False(t, strings.Contains(plan, "Sort Method"),
		"the index already provides the order, sorting 5000 rows per page would not")
}
