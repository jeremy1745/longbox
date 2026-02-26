package archive

import (
	"bytes"
	"fmt"
	"io"

	"github.com/bodgit/sevenzip"
)

type cb7Archive struct {
	reader *sevenzip.ReadCloser
}

func OpenCB7(path string) (Archive, error) {
	r, err := sevenzip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("opening cb7: %w", err)
	}
	return &cb7Archive{reader: r}, nil
}

func (a *cb7Archive) ListEntries() ([]Entry, error) {
	entries := make([]Entry, 0, len(a.reader.File))
	for _, f := range a.reader.File {
		if f.FileInfo().IsDir() {
			continue
		}
		entries = append(entries, Entry{
			Name: f.Name,
			Size: int64(f.UncompressedSize),
		})
	}
	return entries, nil
}

func (a *cb7Archive) ExtractFile(name string) (io.ReadCloser, error) {
	for _, f := range a.reader.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("opening file in cb7: %w", err)
			}
			// Read into memory since sevenzip readers may not support concurrent access
			data, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return nil, fmt.Errorf("reading file from cb7: %w", err)
			}
			return io.NopCloser(bytes.NewReader(data)), nil
		}
	}
	return nil, fmt.Errorf("file not found in archive: %s", name)
}

func (a *cb7Archive) Close() error {
	return a.reader.Close()
}
