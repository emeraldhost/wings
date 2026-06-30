package filesystem

import (
	"bytes"
	"compress/gzip"
	"io"
	"testing"
)

func TestDetectCompressionFormat(t *testing.T) {
	tests := []struct {
		name           string
		data           []byte
		expectedFormat CompressionFormat
	}{
		{
			name:           "GZIP format",
			data:           []byte{0x1F, 0x8B, 0x08, 0x00}, // GZIP magic
			expectedFormat: CompressionGzip,
		},
		{
			name:           "Unknown format defaults to GZIP",
			data:           []byte{0x00, 0x00, 0x00, 0x00},
			expectedFormat: CompressionGzip,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := io.NopCloser(bytes.NewReader(tt.data))
			format, _, err := DetectCompressionFormat(reader)

			if err != nil {
				t.Errorf("DetectCompressionFormat() error = %v", err)
				return
			}

			if format != tt.expectedFormat {
				t.Errorf("DetectCompressionFormat() = %v, want %v", format, tt.expectedFormat)
			}
		})
	}
}

func TestCreateDecompressor(t *testing.T) {
	// Test GZIP decompressor
	t.Run("GZIP decompressor", func(t *testing.T) {
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		_, err := gw.Write([]byte("test data"))
		if err != nil {
			t.Fatal(err)
		}
		gw.Close()

		reader := io.NopCloser(bytes.NewReader(buf.Bytes()))
		decompressor, err := CreateDecompressor(reader, CompressionGzip)
		if err != nil {
			t.Errorf("CreateDecompressor() error = %v", err)
			return
		}
		defer decompressor.Close()

		data, err := io.ReadAll(decompressor)
		if err != nil {
			t.Errorf("Failed to read from GZIP decompressor: %v", err)
			return
		}

		if string(data) != "test data" {
			t.Errorf("GZIP decompression failed: got %s, want 'test data'", string(data))
		}
	})
}
