package link

import (
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/file/internal/usecase"
	"github.com/a-aleesshin/gophprofile/internal/clock"
)

const (
	currentSecret  = "0123456789abcdef0123456789abcdef"
	previousSecret = "fedcba9876543210fedcba9876543210"
	fileID         = "01930000-0000-7000-8000-000000000001"
)

var fixedNow = time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

func testBuilder(t *testing.T, mutate func(*Config)) *Builder {
	t.Helper()

	cfg := Config{PublicBase: "https://domain.com", Secret: currentSecret}

	if mutate != nil {
		mutate(&cfg)
	}

	builder, err := NewBuilder(cfg, clock.Fixed{At: fixedNow})
	require.NoError(t, err)

	return builder
}

func signedLink(t *testing.T, builder *Builder, expiresAt time.Time) (string, VerifyParams) {
	t.Helper()

	raw, err := builder.BuildLink(t.Context(), usecase.BuildLinkParams{
		FileID:    fileID,
		Variant:   "300x300",
		Ext:       "jpg",
		ExpiresAt: expiresAt,
	})
	require.NoError(t, err)

	parsed, err := url.Parse(raw)
	require.NoError(t, err)

	return raw, VerifyParams{
		FileID:  fileID,
		Variant: "300x300",
		Ext:     "jpg",
		Expires: parsed.Query().Get("exp"),
		Sig:     parsed.Query().Get("sig"),
	}
}

func TestBuildLinkForAPublicNamespace(t *testing.T) {
	t.Parallel()

	// Act
	link, err := testBuilder(t, nil).BuildLink(t.Context(), usecase.BuildLinkParams{
		FileID:  fileID,
		Variant: "300x300",
		Ext:     "jpg",
	})

	// Assert
	require.NoError(t, err)
	assert.Equal(t, "https://domain.com/upload/"+fileID+"/300x300.jpg", link)
	assert.NotContains(t, link, "sig=",
		"a public link must stay stable so browsers and caches can keep it")
}

func TestBuildLinkSignsAndVerifies(t *testing.T) {
	t.Parallel()

	// Arrange
	builder := testBuilder(t, nil)
	expiresAt := fixedNow.Add(15 * time.Minute)

	// Act
	raw, params := signedLink(t, builder, expiresAt)

	// Assert
	assert.True(t, strings.HasPrefix(raw, "https://domain.com/upload/"+fileID+"/300x300.jpg?"))
	assert.Equal(t, strconv.FormatInt(expiresAt.Unix(), 10), params.Expires)
	assert.NotEmpty(t, params.Sig)
	assert.NoError(t, builder.Verify(params))
}

func TestVerifyRejectsATamperedLink(t *testing.T) {
	t.Parallel()

	builder := testBuilder(t, nil)
	_, valid := signedLink(t, builder, fixedNow.Add(15*time.Minute))

	tests := []struct {
		name   string
		mutate func(*VerifyParams)
	}{
		{
			name:   "another file",
			mutate: func(p *VerifyParams) { p.FileID = "01930000-0000-7000-8000-000000000002" },
		},
		{
			name:   "another variant",
			mutate: func(p *VerifyParams) { p.Variant = "100x100" },
		},
		{
			name:   "another extension",
			mutate: func(p *VerifyParams) { p.Ext = "png" },
		},
		{
			name: "a later expiry",
			mutate: func(p *VerifyParams) {
				p.Expires = strconv.FormatInt(fixedNow.Add(time.Hour).Unix(), 10)
			},
		},
		{
			name:   "a flipped signature",
			mutate: func(p *VerifyParams) { p.Sig = "A" + p.Sig[1:] },
		},
		{
			name:   "a signature that is not base64url",
			mutate: func(p *VerifyParams) { p.Sig = "not base64!!" },
		},
		{
			name:   "no signature",
			mutate: func(p *VerifyParams) { p.Sig = "" },
		},
		{
			name:   "no expiry",
			mutate: func(p *VerifyParams) { p.Expires = "" },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange
			params := valid
			test.mutate(&params)

			// Act
			err := builder.Verify(params)

			// Assert
			require.ErrorIs(t, err, ErrBadSignature)
			assert.NotErrorIs(t, err, ErrExpired)
		})
	}
}

func TestVerifySeparatesAForgedLinkFromAnExpiredOne(t *testing.T) {
	t.Parallel()

	// Arrange
	builder := testBuilder(t, nil)
	_, expired := signedLink(t, builder, fixedNow.Add(-time.Second))

	forged := expired
	forged.Sig = "A" + forged.Sig[1:]

	// Act
	expiredErr := builder.Verify(expired)
	forgedErr := builder.Verify(forged)

	// Assert
	require.ErrorIs(t, expiredErr, ErrExpired,
		"a client may silently refresh an expired link")
	require.ErrorIs(t, forgedErr, ErrBadSignature,
		"a forged link must never be reported as merely expired")
	assert.NotErrorIs(t, forgedErr, ErrExpired)
}

func TestVerifyAcceptsALinkExactlyAtItsExpiry(t *testing.T) {
	t.Parallel()

	// Arrange
	builder := testBuilder(t, nil)
	_, params := signedLink(t, builder, fixedNow)

	// Act
	err := builder.Verify(params)

	// Assert
	assert.NoError(t, err, "the second it expires is still inside the window")
}

func TestVerifyAcceptsTheRotatedSecret(t *testing.T) {
	t.Parallel()

	// Arrange
	before := testBuilder(t, nil)
	_, issued := signedLink(t, before, fixedNow.Add(15*time.Minute))

	after := testBuilder(t, func(c *Config) {
		c.Secret = previousSecret
		c.PreviousSecret = currentSecret
	})

	// Act
	err := after.Verify(issued)

	// Assert
	require.NoError(t, err, "a link issued before the rotation must survive it")

	_, reissued := signedLink(t, after, fixedNow.Add(15*time.Minute))
	assert.NotEqual(t, issued.Sig, reissued.Sig, "new links are signed with the new secret")

	assert.Error(t, before.Verify(reissued),
		"the old deployment does not know the new secret")
}

func TestVerifyRefusesASeparatorInTheTarget(t *testing.T) {
	t.Parallel()

	builder := testBuilder(t, nil)

	tests := []struct {
		name    string
		fileID  string
		variant string
		ext     string
	}{
		{name: "newline in the file id", fileID: "a\nb", variant: "300x300", ext: "jpg"},
		{name: "slash in the variant", fileID: fileID, variant: "300/300", ext: "jpg"},
		{name: "dot in the variant", fileID: fileID, variant: "300.300", ext: "jpg"},
		{name: "query in the extension", fileID: fileID, variant: "300x300", ext: "jpg?x"},
		{name: "empty extension", fileID: fileID, variant: "300x300", ext: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Act
			_, buildErr := builder.BuildLink(t.Context(), usecase.BuildLinkParams{
				FileID:    test.fileID,
				Variant:   test.variant,
				Ext:       test.ext,
				ExpiresAt: fixedNow.Add(time.Minute),
			})

			verifyErr := builder.Verify(VerifyParams{
				FileID:  test.fileID,
				Variant: test.variant,
				Ext:     test.ext,
				Expires: "1",
				Sig:     "AAAA",
			})

			// Assert
			require.ErrorIs(t, buildErr, ErrInvalidTarget)
			require.ErrorIs(t, verifyErr, ErrBadSignature)
		})
	}
}

func TestSignedMessageCannotBeShifted(t *testing.T) {
	t.Parallel()

	// Arrange
	builder := testBuilder(t, nil)
	expires := strconv.FormatInt(fixedNow.Add(time.Minute).Unix(), 10)

	// Act
	first := sign(builder.signing, fileID, "300x300", "jpg", expires)
	second := sign(builder.signing, fileID, "300x300-jpg", "x", expires)

	// Assert
	assert.NotEqual(t, first, second,
		"two different targets must never share a signature")
}

func TestNewBuilder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		cfg   Config
		clock Clock
	}{
		{
			name: "no clock",
			cfg:  Config{PublicBase: "https://domain.com", Secret: currentSecret},
		},
		{
			name:  "no scheme",
			cfg:   Config{PublicBase: "domain.com", Secret: currentSecret},
			clock: clock.System{},
		},
		{
			name:  "unexpected scheme",
			cfg:   Config{PublicBase: "ftp://domain.com", Secret: currentSecret},
			clock: clock.System{},
		},
		{
			name:  "no host",
			cfg:   Config{PublicBase: "https://", Secret: currentSecret},
			clock: clock.System{},
		},
		{
			name:  "base with a query",
			cfg:   Config{PublicBase: "https://domain.com?x=1", Secret: currentSecret},
			clock: clock.System{},
		},
		{
			name:  "short secret",
			cfg:   Config{PublicBase: "https://domain.com", Secret: "too-short"},
			clock: clock.System{},
		},
		{
			name: "short previous secret",
			cfg: Config{
				PublicBase: "https://domain.com", Secret: currentSecret,
				PreviousSecret: "short",
			},
			clock: clock.System{},
		},
		{
			name: "previous secret equal to the current one",
			cfg: Config{
				PublicBase: "https://domain.com", Secret: currentSecret,
				PreviousSecret: currentSecret,
			},
			clock: clock.System{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Act
			builder, err := NewBuilder(test.cfg, test.clock)

			// Assert
			require.ErrorIs(t, err, ErrInvalidConfig)
			assert.Nil(t, builder)
		})
	}
}

func TestNewBuilderTrimsTheTrailingSlash(t *testing.T) {
	t.Parallel()

	// Act
	builder := testBuilder(t, func(c *Config) { c.PublicBase = "https://domain.com/" })

	link, err := builder.BuildLink(t.Context(), usecase.BuildLinkParams{
		FileID: fileID, Variant: "web", Ext: "jpg",
	})

	// Assert
	require.NoError(t, err)
	assert.Equal(t, "https://domain.com/upload/"+fileID+"/web.jpg", link)
}
