package downloader

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestMaxSizeReaderAllowsExactLimit(t *testing.T) {
	m := &maxSizeReader{r: bytes.NewReader(make([]byte, 10)), remaining: 10}
	n, err := io.Copy(io.Discard, m)
	if err != nil || n != 10 || m.exceeded {
		t.Fatalf("expected 10 bytes without error, got n=%d err=%v exceeded=%v", n, err, m.exceeded)
	}
}

func TestMaxSizeReaderRejectsOversizedBody(t *testing.T) {
	m := &maxSizeReader{r: bytes.NewReader(make([]byte, 11)), remaining: 10}
	_, err := io.Copy(io.Discard, m)
	if !errors.Is(err, ErrFileTooLarge) || !m.exceeded {
		t.Fatalf("expected ErrFileTooLarge, got err=%v exceeded=%v", err, m.exceeded)
	}
}
