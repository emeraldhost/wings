package backup

import (
	"context"
	"io"
	"os"
	"path"

	"emperror.dev/errors"
	"github.com/juju/ratelimit"
	"github.com/mholt/archives"

	"github.com/Rene-Roscher/wings/config"
	"github.com/Rene-Roscher/wings/remote"
	"github.com/Rene-Roscher/wings/server/filesystem"
)

type LocalBackup struct {
	Backup
}

var _ BackupInterface = (*LocalBackup)(nil)

func NewLocal(client remote.Client, uuid string, ignore string) *LocalBackup {
	return &LocalBackup{
		Backup: Backup{
			client:  client,
			Uuid:    uuid,
			Ignore:  ignore,
			adapter: LocalBackupAdapter,
		},
	}
}

// LocateLocal finds the backup for a server and returns the local path. This
// will obviously only work if the backup was created as a local backup.
// ENHANCED: Now supports finding backups with different extensions (backward compatibility)
func LocateLocal(client remote.Client, uuid string) (*LocalBackup, os.FileInfo, error) {
	b := NewLocal(client, uuid, "")
	if err := b.validateIdentifier(); err != nil {
		return nil, nil, err
	}

	// Try current config format first (new behavior)
	st, err := os.Stat(b.Path())
	if err == nil {
		if st.IsDir() {
			return nil, nil, errors.New("invalid archive, is directory")
		}
		return b, st, nil
	}

	// BACKWARD COMPATIBILITY: uncompressed backups are stored as <uuid>.tar.
	if os.IsNotExist(err) {
		tarPath := path.Join(config.Get().System.BackupDirectory, b.Uuid+".tar")
		if st, terr := os.Stat(tarPath); terr == nil {
			if st.IsDir() {
				return nil, nil, errors.New("invalid archive, is directory")
			}
			b.foundPath = tarPath
			return b, st, nil
		}
	}

	return nil, nil, err
}

// Remove removes a backup from the system.
func (b *LocalBackup) Remove() error {
	if err := b.validateIdentifier(); err != nil {
		return err
	}
	return os.Remove(b.Path())
}

// WithLogContext attaches additional context to the log output for this backup.
func (b *LocalBackup) WithLogContext(c map[string]interface{}) {
	b.logContext = c
}

// Generate generates a backup of the selected files and pushes it to the
// defined location for this instance.
func (b *LocalBackup) Generate(ctx context.Context, fsys *filesystem.Filesystem, ignore string) (*ArchiveDetails, error) {
	if err := b.validateIdentifier(); err != nil {
		return nil, err
	}
	a := &filesystem.Archive{
		Filesystem: fsys,
		Ignore:     ignore,
	}

	b.log().WithField("path", b.Path()).Info("creating backup for server")
	if err := a.Create(ctx, b.Path()); err != nil {
		return nil, err
	}
	b.log().Info("created backup successfully")

	ad, err := b.Details(ctx, nil)
	if err != nil {
		return nil, errors.WrapIf(err, "backup: failed to get archive details for local backup")
	}
	return ad, nil
}

// Restore will walk over the archive and call the callback function for each
// file encountered.
func (b *LocalBackup) Restore(ctx context.Context, _ io.Reader, callback RestoreCallback) error {
	if err := b.validateIdentifier(); err != nil {
		return err
	}
	f, err := os.Open(b.Path())
	if err != nil {
		return err
	}
	defer f.Close()

	var reader io.Reader = f
	// Steal the logic we use for making backups which will be applied when restoring
	// this specific backup. This allows us to prevent overloading the disk unintentionally.
	if writeLimit := int64(config.Get().System.Backups.WriteLimit * 1024 * 1024); writeLimit > 0 {
		reader = ratelimit.Reader(f, ratelimit.NewBucketWithRate(float64(writeLimit), writeLimit))
	}

	// Wrap reader in NopCloser to satisfy ReadCloser interface
	readCloser := io.NopCloser(reader)

	// Auto-detect compression format and decompress
	format, detectedReader, err := filesystem.DetectCompressionFormat(readCloser)
	if err != nil {
		return errors.WrapIf(err, "failed to detect backup compression format")
	}

	decompressedReader, err := filesystem.CreateDecompressor(detectedReader, format)
	if err != nil {
		return errors.WrapIf(err, "failed to create decompressor for backup")
	}
	defer decompressedReader.Close()

	// Use the mholt/archives package to extract TAR archive
	tarFormat := archives.Tar{}
	if err := tarFormat.Extract(ctx, decompressedReader, func(ctx context.Context, f archives.FileInfo) error {
		r, err := f.Open()
		if err != nil {
			return err
		}
		defer r.Close()

		return callback(f.NameInArchive, f.FileInfo, r)
	}); err != nil {
		return err
	}
	return nil
}
