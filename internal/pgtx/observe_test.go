package pgtx

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestObservePoolRefusesAMissingPool(t *testing.T) {
	t.Parallel()

	err := ObservePool("postgres", nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidConfig)
}

func TestObservePoolRefusesAnEmptyName(t *testing.T) {
	t.Parallel()

	assert.Error(t, ObservePool("", nil))
}
