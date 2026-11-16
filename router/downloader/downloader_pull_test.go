package downloader

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/emeraldhost/wings/config"
	"github.com/emeraldhost/wings/remote"
	"github.com/emeraldhost/wings/server"
)

const pullTestServerUuid = "ffffffff-ffff-4fff-8fff-ffffffffffff"

// newPullTestServer returns a server whose files are stored in a temporary data
// directory and owned by the user running the tests, so that they pass without
// root. Loopback is allowed so that the tests can use a local origin.
func newPullTestServer(t *testing.T) *server.Server {
	t.Helper()
	previous := config.Get()
	t.Cleanup(func() { config.Set(previous) })
	next := *previous
	next.System.Data = t.TempDir()
	next.System.User.Uid = os.Getuid()
	next.System.User.Gid = os.Getgid()
	next.Api.RemoteDownloadAllowlist = []string{"127.0.0.1"}
	config.Set(&next)

	settings, err := json.Marshal(map[string]string{"uuid": pullTestServerUuid})
	require.NoError(t, err)
	s, err := server.NewEmptyManager(nil).InitServer(pullTestServerUuid, remote.ServerConfigurationResponse{
		Settings:             settings,
		ProcessConfiguration: &remote.ProcessConfiguration{},
	})
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(s.Filesystem().Path(), 0o755))
	return s
}

// A file from a server that compresses responses for clients that accept gzip
// can be pulled.
func TestDownloadAcceptsGzipEncodedResponses(t *testing.T) {
	s := newPullTestServer(t)

	payload := []byte(strings.Repeat("pull test payload\n", 4096))
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	_, err := zw.Write(payload)
	require.NoError(t, err)
	require.NoError(t, zw.Close())

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := payload
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			w.Header().Set("Content-Encoding", "gzip")
			body = compressed.Bytes()
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write(body)
	}))
	t.Cleanup(origin.Close)

	u, err := url.Parse(origin.URL + "/payload.bin")
	require.NoError(t, err)
	require.NoError(t, New(s, DownloadRequest{Directory: "/", URL: u, FileName: "payload.bin"}).Execute())

	got, err := os.ReadFile(filepath.Join(s.Filesystem().Path(), "payload.bin"))
	require.NoError(t, err)
	require.Equal(t, payload, got)
}

// Only the name of a file from Content-Disposition is used, so the file is saved
// in the directory that was asked for.
func TestDownloadFilenameFromHeaderIsSavedInRequestedDirectory(t *testing.T) {
	tests := []struct {
		directory string
		want      string
	}{
		{directory: "/chosen/dir", want: "chosen/dir/named.txt"},
		{directory: "chosen/dir", want: "chosen/dir/named.txt"},
		{directory: "", want: "named.txt"},
	}
	for _, tt := range tests {
		t.Run(tt.directory, func(t *testing.T) {
			s := newPullTestServer(t)

			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Disposition", `attachment; filename="../../named.txt"`)
				w.Header().Set("Content-Length", "5")
				_, _ = w.Write([]byte("hello"))
			}))
			t.Cleanup(origin.Close)

			u, err := url.Parse(origin.URL + "/file")
			require.NoError(t, err)
			require.NoError(t, New(s, DownloadRequest{Directory: tt.directory, URL: u, FileName: "file", UseHeader: true}).Execute())

			root := s.Filesystem().Path()
			require.FileExists(t, filepath.Join(root, tt.want))
			require.NoFileExists(t, filepath.Join(filepath.Dir(root), "named.txt"))
		})
	}
}

// A download that fails to connect is reported as a failed download.
func TestDownloadConnectionFailureIsDownloadError(t *testing.T) {
	s := newPullTestServer(t)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())

	u, err := url.Parse("http://" + addr + "/file")
	require.NoError(t, err)
	err = New(s, DownloadRequest{Directory: "/", URL: u, FileName: "file"}).Execute()
	require.Error(t, err)
	require.True(t, IsDownloadError(err), "got %v", err)
	require.ErrorIs(t, err, ErrDownloadFailed)
}

// pullEvents collects the "download progress" events published for s while the
// test runs. Publish only returns once an event is in the channel, so all events
// of a finished Execute call can be read without waiting.
func pullEvents(t *testing.T, s *server.Server) func() []DownloadProgressUpdate {
	t.Helper()
	ch := make(chan []byte, 256)
	s.Events().On(ch)
	t.Cleanup(func() { s.Events().Off(ch) })
	return func() []DownloadProgressUpdate {
		var out []DownloadProgressUpdate
		for {
			select {
			case b := <-ch:
				var e struct {
					Topic string
					Data  DownloadProgressUpdate
				}
				require.NoError(t, json.Unmarshal(b, &e))
				if e.Topic == server.DownloadProgressEvent {
					out = append(out, e.Data)
				}
			default:
				return out
			}
		}
	}
}

const plusFilename = "fabric-language-kotlin-1.14.2+kotlin.2.4.21.jar"

// A background pull of a file with "+" in its name (common for mods) is saved
// and reported as downloading, then completed.
func TestDownloadWithPlusInFilenameSendsEvents(t *testing.T) {
	s := newPullTestServer(t)
	events := pullEvents(t, s)

	payload := []byte(strings.Repeat("kotlin\n", 1024))
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		_, _ = w.Write(payload)
	}))
	t.Cleanup(origin.Close)

	u, err := url.Parse(origin.URL + "/data/versions/fabric-language-kotlin-1.14.2%2Bkotlin.2.4.21.jar")
	require.NoError(t, err)
	dl := New(s, DownloadRequest{Directory: "mods", URL: u, FileName: plusFilename})
	dl.EnableEvents()
	require.NoError(t, dl.Execute())

	got, err := os.ReadFile(filepath.Join(s.Filesystem().Path(), "mods", plusFilename))
	require.NoError(t, err)
	require.Equal(t, payload, got)

	evs := events()
	require.GreaterOrEqual(t, len(evs), 2)
	first, last := evs[0], evs[len(evs)-1]
	require.Equal(t, "downloading", first.Status)
	require.Equal(t, "completed", last.Status)
	require.Equal(t, 100, last.Percentage)
	require.Equal(t, plusFilename, last.Filename)
	require.Equal(t, dl.Identifier, last.Identifier)
	require.Equal(t, int64(len(payload)), last.BytesTotal)
	require.Empty(t, last.Error)
	for _, ev := range evs[:len(evs)-1] {
		require.Equal(t, "downloading", ev.Status)
	}
}

// requireSingleFailure checks that exactly one event was sent, a failed one with
// the given reason, that the client can match to the download.
func requireSingleFailure(t *testing.T, evs []DownloadProgressUpdate, dl *Download, filename, reason string) {
	t.Helper()
	require.Len(t, evs, 1, "events: %+v", evs)
	ev := evs[0]
	require.Equal(t, "failed", ev.Status)
	require.Equal(t, -1, ev.Percentage)
	require.Equal(t, reason, ev.Error)
	require.Equal(t, dl.Identifier, ev.Identifier)
	require.Equal(t, filename, ev.Filename)
	require.Zero(t, ev.BytesTotal)
}

// A download the remote server answers with an error status is reported as
// failed instead of leaving the client waiting.
func TestDownloadBadStatusSendsFailedEvent(t *testing.T) {
	s := newPullTestServer(t)
	events := pullEvents(t, s)

	origin := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(origin.Close)

	u, err := url.Parse(origin.URL + "/missing.jar")
	require.NoError(t, err)
	dl := New(s, DownloadRequest{Directory: "mods", URL: u, FileName: plusFilename})
	dl.EnableEvents()
	err = dl.Execute()
	require.ErrorIs(t, err, ErrBadStatus)

	requireSingleFailure(t, events(), dl, plusFilename, failureBadStatus)
}

// A download with a filename that is refused is reported as failed, with the
// name that was asked for.
func TestDownloadInvalidFilenameSendsFailedEvent(t *testing.T) {
	s := newPullTestServer(t)
	events := pullEvents(t, s)

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "5")
		_, _ = w.Write([]byte("hello"))
	}))
	t.Cleanup(origin.Close)

	u, err := url.Parse(origin.URL + "/file")
	require.NoError(t, err)
	dl := New(s, DownloadRequest{Directory: "/", URL: u, FileName: "a b.jar"})
	dl.EnableEvents()
	err = dl.Execute()
	require.ErrorIs(t, err, ErrInvalidFilename)

	evs := events()
	require.Len(t, evs, 1, "events: %+v", evs)
	require.Equal(t, "failed", evs[0].Status)
	require.Equal(t, failureInvalidFilename, evs[0].Error)
	require.Equal(t, "a b.jar", evs[0].Filename)
	require.Equal(t, dl.Identifier, evs[0].Identifier)
	require.NoFileExists(t, filepath.Join(s.Filesystem().Path(), "a b.jar"))
}

// A download that fails before any response arrived is reported as failed, with
// the name taken from the URL when none was asked for.
func TestDownloadConnectionFailureSendsFailedEvent(t *testing.T) {
	s := newPullTestServer(t)
	events := pullEvents(t, s)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())

	u, err := url.Parse("http://" + addr + "/dir/server.jar")
	require.NoError(t, err)
	dl := New(s, DownloadRequest{Directory: "/", URL: u})
	dl.EnableEvents()
	require.ErrorIs(t, dl.Execute(), ErrDownloadFailed)

	requireSingleFailure(t, events(), dl, "server.jar", failureRequestFailed)
}

// A download to an internal address is reported as blocked.
func TestDownloadBlockedSendsFailedEvent(t *testing.T) {
	s := newPullTestServer(t)
	setAllowlist(t)
	events := pullEvents(t, s)

	u, err := url.Parse("http://127.0.0.1:1/file.jar")
	require.NoError(t, err)
	dl := New(s, DownloadRequest{Directory: "/", URL: u, FileName: "file.jar"})
	dl.EnableEvents()
	require.ErrorIs(t, dl.Execute(), ErrInternalResolution)

	requireSingleFailure(t, events(), dl, "file.jar", failureBlocked)
}

// Downloads without events enabled (foreground pulls) publish nothing.
func TestDownloadWithoutEventsPublishesNothing(t *testing.T) {
	s := newPullTestServer(t)
	events := pullEvents(t, s)

	origin := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(origin.Close)

	u, err := url.Parse(origin.URL + "/missing.jar")
	require.NoError(t, err)
	require.Error(t, New(s, DownloadRequest{Directory: "/", URL: u, FileName: "missing.jar"}).Execute())
	require.Empty(t, events())
}

// A download cancelled through the API while it is running is reported as
// cancelled, not as a write or request failure.
func TestDownloadCancelledSendsCancelledEvent(t *testing.T) {
	s := newPullTestServer(t)
	events := pullEvents(t, s)

	release := make(chan struct{})
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1048576")
		_, _ = w.Write(bytes.Repeat([]byte("x"), 1024))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); origin.Close() })

	u, err := url.Parse(origin.URL + "/big.jar")
	require.NoError(t, err)
	dl := New(s, DownloadRequest{Directory: "/", URL: u, FileName: "big.jar"})
	dl.EnableEvents()
	done := make(chan error, 1)
	go func() { done <- dl.Execute() }()

	require.Eventually(t, func() bool { return dl.Progress() > 0 }, 5*time.Second, 5*time.Millisecond)
	dl.Cancel()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled download did not return")
	}

	evs := events()
	require.NotEmpty(t, evs)
	last := evs[len(evs)-1]
	require.Equal(t, "failed", last.Status)
	require.Equal(t, failureCancelled, last.Error)
	require.Equal(t, "big.jar", last.Filename)
}
