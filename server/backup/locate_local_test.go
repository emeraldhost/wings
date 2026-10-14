package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Rene-Roscher/wings/config"
)

// A legacy uncompressed <uuid>.tar backup found by LocateLocal must be the file
// that Size, Checksum and Details read, not the default <uuid>.tar.gz path.
func TestLocateLocalTarBackupDetails(t *testing.T) {
	backupDir := t.TempDir()
	config.Set(&config.Configuration{
		AuthenticationToken: "test-token",
		System: config.SystemConfiguration{
			BackupDirectory: backupDir,
		},
	})

	uuid := "11111111-1111-1111-1111-111111111111"
	tarPath := filepath.Join(backupDir, uuid+".tar")
	if err := os.WriteFile(tarPath, []byte("archive"), 0o600); err != nil {
		t.Fatal(err)
	}

	b, st, err := LocateLocal(nil, uuid)
	if err != nil {
		t.Fatalf("LocateLocal: %v", err)
	}
	if b.Path() != tarPath {
		t.Fatalf("expected path %q, got %q", tarPath, b.Path())
	}
	if st.Size() != 7 {
		t.Fatalf("expected stat size 7, got %d", st.Size())
	}

	ad, err := b.Details(context.Background(), nil)
	if err != nil {
		t.Fatalf("Details: %v", err)
	}
	if ad.Size != 7 || ad.ChecksumType != "sha256" || ad.Checksum == "" {
		t.Fatalf("unexpected details: %+v", ad)
	}
}
