package archive

import (
	"archive/zip"
	"fmt"
	"io"
)

type cbzArchive struct {
	reader *zip.ReadCloser
}

func OpenCBZ(path string) (Archive, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("opening cbz: %w", err)
	}
	return &cbzArchive{reader: r}, nil
}

func (a *cbzArchive) ListEntries() ([]Entry, error) {
	entries := make([]Entry, 0, len(a.reader.File))
	for _, f := range a.reader.File {
		if f.FileInfo().IsDir() {
			continue
		}
		entries = append(entries, Entry{
			Name: f.Name,
			Size: int64(f.UncompressedSize64),
		})
	}
	return entries, nil
}

func (a *cbzArchive) ExtractFile(name string) (io.ReadCloser, error) {
	for _, f := range a.reader.File {
		if f.Name == name {
			return f.Open()
		}
	}
	return nil, fmt.Errorf("file not found in archive: %s", name)
}

func (a *cbzArchive) Close() error {
	return a.reader.Close()
}
