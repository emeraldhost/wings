package downloader

import (
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSanitizeFilename(t *testing.T) {
	for _, name := range []string{
		"fabric-language-kotlin-1.14.2+kotlin.2.4.21.jar",
		"sodium-fabric-0.6.0+mc1.21.jar",
		"server.jar",
		"world_backup-2024.zip",
		strings.Repeat("a", 251) + ".jar",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := sanitizeFilename(name)
			require.NoError(t, err)
			require.Equal(t, name, got)
		})
	}

	for _, name := range []string{
		"",
		".hidden",
		"a b.jar",
		"/etc/passwd",
		`a\b.jar`,
		"x..jar",
		strings.Repeat("a", 252) + ".jar",
		"a;b.jar",
		"a%2Bb.jar",
		"a$b.jar",
		"a`b.jar",
		`a"b.jar`,
		"a'b.jar",
		"a\x00b.jar",
		"a\nb.jar",
		".",
		"..",
		"/",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := sanitizeFilename(name)
			require.True(t, errors.Is(err, ErrInvalidFilename), "expected %q to be rejected, got %v", name, err)
		})
	}
}

// Directory components of a relative name are dropped rather than rejected
// (filepath.Base), so a name can never point outside the directory that was
// asked for. Absolute names are rejected outright.
func TestSanitizeFilenameDropsDirectories(t *testing.T) {
	for name, want := range map[string]string{
		"../x.jar":         "x.jar",
		"a/b.jar":          "b.jar",
		"mods/../../x.jar": "x.jar",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := sanitizeFilename(name)
			require.NoError(t, err)
			require.Equal(t, want, got)
		})
	}
	_, err := sanitizeFilename("../../.hidden.jar")
	require.ErrorIs(t, err, ErrInvalidFilename)
}

// Logged request errors carry only the host, not the full URL with its query.
func TestLoggableErrorOmitsURL(t *testing.T) {
	err := &url.Error{Op: "Get", URL: "https://cdn.example.com/f.jar?X-Amz-Signature=secret", Err: errors.New("connection refused")}
	got := loggableError(err)
	require.Equal(t, "Get cdn.example.com: connection refused", got)
	require.NotContains(t, got, "secret")
}
