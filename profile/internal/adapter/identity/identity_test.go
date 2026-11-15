package identity

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/internal/clock"
	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
)

const (
	testSecret   = "0123456789abcdef0123456789abcdef"
	testPrevious = "aaaabbbbccccddddeeeeffff00001111"
	testLogin    = "fedcba9876543210fedcba9876543210"
)

var testNow = time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)

func testConfig() Config {
	return Config{
		Secret:      testSecret,
		DemoLogin:   true,
		LoginSecret: testLogin,
		TTL:         time.Hour,
	}
}

func testUser(t *testing.T, raw string) domain.UserID {
	t.Helper()

	user, err := domain.NewUserID(raw)
	require.NoError(t, err)

	return user
}

func testSigner(t *testing.T, cfg Config, at time.Time) *Signer {
	t.Helper()

	signer, err := New(cfg, clock.Fixed{At: at})
	require.NoError(t, err)

	return signer
}

func TestIssuedTokenNamesItsUser(t *testing.T) {
	t.Parallel()

	// Arrange
	signer := testSigner(t, testConfig(), testNow)
	user := testUser(t, "user-1")

	// Act
	token, expires, err := signer.Issue(user)
	require.NoError(t, err)

	got, err := signer.Verify(token)

	// Assert
	require.NoError(t, err)
	assert.True(t, got.Equal(user))
	assert.Equal(t, testNow.Add(time.Hour), expires)
	assert.NotContains(t, token, "user-1",
		"the payload is encoded, so the id is not readable at a glance in a log")
}

func TestVerifyRefusesWhatItCannotTrust(t *testing.T) {
	t.Parallel()

	signer := testSigner(t, testConfig(), testNow)

	valid, _, err := signer.Issue(testUser(t, "user-1"))
	require.NoError(t, err)

	payload, signature, _ := strings.Cut(valid, ".")

	other := testSigner(t, Config{
		Secret:      testPrevious,
		DemoLogin:   true,
		LoginSecret: testLogin,
		TTL:         time.Hour,
	}, testNow)

	foreign, _, err := other.Issue(testUser(t, "user-1"))
	require.NoError(t, err)

	tampered, _, err := signer.Issue(testUser(t, "user-2"))
	require.NoError(t, err)

	tamperedPayload, _, _ := strings.Cut(tampered, ".")

	tests := map[string]string{
		"empty":                      "",
		"no signature":               payload,
		"no payload":                 "." + signature,
		"signed by another secret":   foreign,
		"payload swapped for a peer": tamperedPayload + "." + signature,
		"signature truncated":        payload + "." + signature[:len(signature)-2],
		"payload not base64url":      "not!base64." + signature,
	}

	for name, token := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			user, err := signer.Verify(token)

			require.Error(t, err)
			assert.True(t, user.IsZero())
		})
	}
}

func TestVerifyRefusesAnExpiredToken(t *testing.T) {
	t.Parallel()

	// Arrange
	issuer := testSigner(t, testConfig(), testNow)

	token, _, err := issuer.Issue(testUser(t, "user-1"))
	require.NoError(t, err)

	// Act: the same secret, one second past the hour.
	later := testSigner(t, testConfig(), testNow.Add(time.Hour+time.Second))

	_, err = later.Verify(token)

	// Assert
	require.ErrorIs(t, err, ErrExpired,
		"a token that outlives its window is how a revoked user keeps working")
}

func TestVerifyStillAcceptsThePreviousSecret(t *testing.T) {
	t.Parallel()

	// Arrange
	old := testSigner(t, Config{
		Secret:      testPrevious,
		DemoLogin:   true,
		LoginSecret: testLogin,
		TTL:         time.Hour,
	}, testNow)

	token, _, err := old.Issue(testUser(t, "user-1"))
	require.NoError(t, err)

	rotated := testSigner(t, Config{
		Secret:         testSecret,
		PreviousSecret: testPrevious,
		DemoLogin:      true,
		LoginSecret:    testLogin,
		TTL:            time.Hour,
	}, testNow)

	// Act
	user, err := rotated.Verify(token)

	// Assert
	require.NoError(t, err, "rotation must not sign every open session out")
	assert.Equal(t, "user-1", user.String())

	fresh, _, err := rotated.Issue(testUser(t, "user-2"))
	require.NoError(t, err)

	_, err = old.Verify(fresh)
	require.Error(t, err, "the new secret signs, the old one only verifies")
}

func TestLoginChecksTheSharedSecret(t *testing.T) {
	t.Parallel()

	signer := testSigner(t, testConfig(), testNow)

	require.NoError(t, signer.Login(testLogin))
	require.ErrorIs(t, signer.Login(testSecret), ErrBadSecret,
		"the signing secret is not a password and must not work as one")
	require.ErrorIs(t, signer.Login(""), ErrBadSecret)
	require.ErrorIs(t, signer.Login(testLogin+"x"), ErrBadSecret)
}

func TestDemoLoginIsClosedByDefault(t *testing.T) {
	t.Parallel()

	cfg := testConfig()
	cfg.DemoLogin = false
	cfg.LoginSecret = ""

	signer := testSigner(t, cfg, testNow)

	assert.False(t, signer.DemoLoginEnabled(),
		"the demo door must stay shut unless someone opens it on purpose")
	require.ErrorIs(t, signer.Login(testLogin), ErrLoginDisabled)
	require.ErrorIs(t, signer.Login(""), ErrLoginDisabled,
		"a closed door answers the same whatever is offered to it")

	user := testUser(t, "user-1")

	token, _, err := signer.Issue(user)
	require.NoError(t, err,
		"closing the demo door must not break the tokens the service already signs")

	got, err := signer.Verify(token)
	require.NoError(t, err)
	assert.True(t, got.Equal(user))
}

func TestNewRefusesABrokenConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*Config)
		clock  Clock
	}{
		{name: "no clock"},
		{
			name:   "short secret",
			mutate: func(c *Config) { c.Secret = "too-short" },
		},
		{
			name:   "short previous secret",
			mutate: func(c *Config) { c.PreviousSecret = "too-short" },
		},
		{
			name:   "short login secret with the demo door open",
			mutate: func(c *Config) { c.LoginSecret = "too-short" },
		},
		{
			name:   "no login secret with the demo door open",
			mutate: func(c *Config) { c.LoginSecret = "" },
		},
		{
			name:   "login secret with the demo door closed",
			mutate: func(c *Config) { c.DemoLogin = false },
		},
		{
			name:   "login secret equals the signing secret",
			mutate: func(c *Config) { c.LoginSecret = testSecret },
		},
		{
			name:   "no ttl",
			mutate: func(c *Config) { c.TTL = 0 },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			cfg := testConfig()
			if test.mutate != nil {
				test.mutate(&cfg)
			}

			signer, err := New(cfg, test.clock)

			require.ErrorIs(t, err, ErrInvalidConfig)
			assert.Nil(t, signer)
		})
	}
}

func TestIssueRefusesAnEmptyUser(t *testing.T) {
	t.Parallel()

	signer := testSigner(t, testConfig(), testNow)

	_, _, err := signer.Issue(domain.UserID{})
	require.ErrorIs(t, err, ErrBadToken)
}
