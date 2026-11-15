package domain

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func canonicalOriginalNames() []string {
	return []string{
		"doc.pdf",
		"Doc.PDF",
		"my report (final).pdf",
		"archive.tar.gz",
		"a",
		".hidden",
		"doc.",
		"...",
		"отчёт за 2026 год.pdf",
		"写真.jpeg",
		"photo 📷.png",
		`say "hi".txt`,
		"100%_done;ok.csv",
		"a:b|c<d>e?f*g.txt",
		strings.Repeat("x", maxNameLength),
	}
}

func TestNewOriginalName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "plain name", input: "doc.pdf", want: "doc.pdf"},
		{name: "case is preserved", input: "Doc.PDF", want: "Doc.PDF"},
		{name: "inner spaces are preserved", input: "my report.pdf", want: "my report.pdf"},
		{name: "surrounded by spaces", input: "  doc.pdf  ", want: "doc.pdf"},
		{name: "surrounded by tabs and newlines", input: "\t\n doc.pdf \r\n", want: "doc.pdf"},
		{name: "non-breaking space is trimmed", input: " doc.pdf ", want: "doc.pdf"},
		{name: "cyrillic", input: "отчёт.pdf", want: "отчёт.pdf"},
		{name: "cjk", input: "写真.jpeg", want: "写真.jpeg"},
		{name: "emoji", input: "photo 📷.png", want: "photo 📷.png"},
		{name: "leading dot", input: ".hidden", want: ".hidden"},
		{name: "trailing dot", input: "doc.", want: "doc."},
		{name: "three dots", input: "...", want: "..."},
		{name: "double extension", input: "archive.tar.gz", want: "archive.tar.gz"},
		{name: "single character", input: "a", want: "a"},
		{name: "quotes", input: `say "hi".txt`, want: `say "hi".txt`},
		{name: "windows reserved characters", input: "a:b|c<d>e?f*g.txt", want: "a:b|c<d>e?f*g.txt"},
		{name: "at the byte limit", input: strings.Repeat("x", maxNameLength), want: strings.Repeat("x", maxNameLength)},
		{
			name:  "padding does not count towards the limit",
			input: "   " + strings.Repeat("x", maxNameLength) + "   ",
			want:  strings.Repeat("x", maxNameLength),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := NewOriginalName(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got.String())
			assert.False(t, got.IsZero())
		})
	}
}

func TestNewOriginalName_Errors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		input  string
		substr string
	}{
		{name: "empty", input: "", substr: "name is required"},
		{name: "spaces only", input: "   ", substr: "name is required"},
		{name: "tabs and newlines only", input: "\t\r\n ", substr: "name is required"},
		{
			name:   "one byte over the limit",
			input:  strings.Repeat("x", maxNameLength+1),
			substr: "name exceeds",
		},
		{
			name:   "limit counts bytes not runes",
			input:  strings.Repeat("я", maxNameLength/2+1),
			substr: "name exceeds",
		},
		{name: "current directory", input: ".", substr: `must not be "."`},
		{name: "parent directory", input: "..", substr: `must not be ".."`},
		{name: "parent directory padded", input: "  ..  ", substr: `must not be ".."`},
		{name: "forward slash", input: "a/b.pdf", substr: "path separators"},
		{name: "backslash", input: `a\b.pdf`, substr: "path separators"},
		{name: "leading slash", input: "/doc.pdf", substr: "path separators"},
		{name: "traversal", input: "../../etc/passwd", substr: "path separators"},
		{name: "nul byte", input: "doc\x00.pdf", substr: "control character"},
		{name: "carriage return and line feed", input: "doc.pdf\r\nX-Injected: 1", substr: "control character"},
		{name: "line feed only", input: "doc\n.pdf", substr: "control character"},
		{name: "carriage return only", input: "doc\r.pdf", substr: "control character"},
		{name: "inner tab", input: "doc\t.pdf", substr: "control character"},
		{name: "vertical tab", input: "doc\v.pdf", substr: "control character"},
		{name: "escape", input: "doc\x1b[31m.pdf", substr: "control character"},
		{name: "delete", input: "doc\x7f.pdf", substr: "control character"},
		{name: "c1 next line", input: "doc\u0085.pdf", substr: "control character"},
		{name: "invalid utf-8", input: "doc\xff\xfe.pdf", substr: "not valid UTF-8"},
		{name: "lone surrogate bytes", input: "doc\xed\xa0\x80.pdf", substr: "not valid UTF-8"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := NewOriginalName(tt.input)
			require.Error(t, err)
			require.ErrorIs(t, err, ErrInvalidOriginalName)
			assert.Contains(t, err.Error(), tt.substr)
			assert.True(t, got.IsZero(), "a rejected name must not be returned")
		})
	}
}

func TestRestoreOriginalName(t *testing.T) {
	t.Parallel()

	for _, value := range canonicalOriginalNames() {
		t.Run(value, func(t *testing.T) {
			t.Parallel()

			got, err := RestoreOriginalName(value)
			require.NoError(t, err)
			assert.Equal(t, value, got.String())
		})
	}
}

func TestRestoreOriginalName_RejectsNonCanonical(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{name: "leading space", input: " doc.pdf"},
		{name: "trailing space", input: "doc.pdf "},
		{name: "trailing newline", input: "doc.pdf\n"},
		{name: "leading tab", input: "\tdoc.pdf"},
		{name: "spaces only", input: "   "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := RestoreOriginalName(tt.input)
			require.Error(t, err)
			require.ErrorIs(t, err, ErrInvalidOriginalName)
			assert.Contains(t, err.Error(), "not canonical")
			assert.True(t, got.IsZero())
		})
	}
}

func TestRestoreOriginalName_Errors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		input  string
		substr string
	}{
		{name: "empty", input: "", substr: "name is required"},
		{name: "too long", input: strings.Repeat("x", maxNameLength+1), substr: "name exceeds"},
		{name: "parent directory", input: "..", substr: `must not be ".."`},
		{name: "forward slash", input: "a/b.pdf", substr: "path separators"},
		{name: "line feed", input: "doc\nX-Injected: 1", substr: "control character"},
		{name: "invalid utf-8", input: "doc\xff.pdf", substr: "not valid UTF-8"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := RestoreOriginalName(tt.input)
			require.Error(t, err)
			require.ErrorIs(t, err, ErrInvalidOriginalName)
			assert.Contains(t, err.Error(), tt.substr)
			assert.True(t, got.IsZero())
		})
	}
}

func TestOriginalName_RoundTripThroughRestore(t *testing.T) {
	t.Parallel()

	inputs := append(canonicalOriginalNames(), "  doc.pdf  ", "\n отчёт.pdf \t")

	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			t.Parallel()

			created, err := NewOriginalName(input)
			require.NoError(t, err)

			restored, err := RestoreOriginalName(created.String())
			require.NoError(t, err, "a name produced by NewOriginalName must be canonical")
			assert.True(t, restored.Equal(created), "got %q, want %q", restored, created)
		})
	}
}

func TestOriginalName_Equal(t *testing.T) {
	t.Parallel()

	doc, err := NewOriginalName("doc.pdf")
	require.NoError(t, err)

	same, err := NewOriginalName("  doc.pdf  ")
	require.NoError(t, err)

	other, err := NewOriginalName("photo.png")
	require.NoError(t, err)

	upper, err := NewOriginalName("Doc.pdf")
	require.NoError(t, err)

	assert.True(t, doc.Equal(doc))
	assert.True(t, doc.Equal(same), "trimming must not change identity")
	assert.False(t, doc.Equal(other))
	assert.False(t, doc.Equal(upper), "comparison must be case-sensitive")
	assert.False(t, doc.Equal(OriginalName{}))
}

func TestOriginalName_Zero(t *testing.T) {
	t.Parallel()

	var zero OriginalName

	assert.True(t, zero.IsZero())
	assert.Empty(t, zero.String())
	assert.True(t, zero.Equal(OriginalName{}))

	doc, err := NewOriginalName("doc.pdf")
	require.NoError(t, err)
	assert.False(t, doc.IsZero())
}
