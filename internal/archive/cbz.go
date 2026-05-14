package archive

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
)

type cbzArchive struct {
	reader *zip.ReadCloser
	// memReader is set when the archive was opened from an in-memory byte
	// slice (OpenCBZBytes). Mutually exclusive with reader. Avoids a
	// second OS file open after processFile has already read the file
	// to compute its hash.
	memReader *zip.Reader
}

func OpenCBZ(path string) (Archive, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("opening cbz: %w", err)
	}
	return &cbzArchive{reader: r}, nil
}

// OpenCBZBytes opens a CBZ archive backed by an in-memory byte slice.
// Used by single-pass scan paths that hash the file and immediately want
// to inspect the archive without re-reading from disk.
func OpenCBZBytes(b []byte) (Archive, error) {
	r, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return nil, fmt.Errorf("opening cbz from bytes: %w", err)
	}
	return &cbzArchive{memReader: r}, nil
}

// files returns the underlying *zip.File slice from whichever backing
// reader is in use.
func (a *cbzArchive) files() []*zip.File {
	if a.reader != nil {
		return a.reader.File
	}
	if a.memReader != nil {
		return a.memReader.File
	}
	return nil
}

func (a *cbzArchive) ListEntries() ([]Entry, error) {
	files := a.files()
	entries := make([]Entry, 0, len(files))
	for _, f := range files {
		if f.FileInfo().IsDir() || !isSafeEntryName(f.Name) {
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
	if !isSafeEntryName(name) {
		return nil, fmt.Errorf("unsafe entry name: %s", name)
	}
	for _, f := range a.files() {
		if f.Name == name {
			return f.Open()
		}
	}
	return nil, fmt.Errorf("file not found in archive: %s", name)
}

func (a *cbzArchive) Close() error {
	if a.reader != nil {
		return a.reader.Close()
	}
	return nil // mem reader has no resources to release
}
