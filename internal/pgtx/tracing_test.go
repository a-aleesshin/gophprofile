package pgtx

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOperationOfNamesTheVerb(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"SELECT 1":                       "SELECT",
		"select 1":                       "SELECT",
		"\n\t\tSELECT id FROM uploads\n": "SELECT",
		"  update outbox set x = 1":      "UPDATE",
		"INSERT INTO files (id)":         "INSERT",
		"DELETE FROM outbox":             "DELETE",
		"begin":                          "BEGIN",
		"commit":                         "COMMIT",
		"WITH source AS (SELECT 1)":      "WITH",
		"":                               unknownOperation,
		"   ":                            unknownOperation,
		"-- a comment first":             unknownOperation,
		"$1":                             unknownOperation,
	}

	for sql, want := range tests {
		assert.Equal(t, want, operationOf(sql), "sql: %q", sql)
	}
}

func TestOperationOfStaysLowCardinality(t *testing.T) {
	t.Parallel()

	first := operationOf("SELECT id FROM uploads WHERE id = $1")
	second := operationOf("SELECT id FROM files WHERE owner_ref = $1")

	assert.Equal(t, first, second,
		"the span name groups operations; the statement goes to an attribute instead")
}
