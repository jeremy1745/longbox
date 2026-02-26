package archive

import (
	"bytes"
	"fmt"
	"io"
	"os"

	"github.com/nwaples/rardecode/v2"
)

type cbrArchive struct {
	path string
}

func OpenCBR(path string) (Archive, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("opening cbr: %w", err)
	}
	return &cbrArchive{path: path}, nil
}

func (a *cbrArchive) ListEntries() ([]Entry, error) {
	r, err := rardecode.OpenReader(a.path)
	if err != nil {
		return nil, fmt.Errorf("reading cbr: %w", err)
	}
	defer r.Close()

	var entries []Entry
	for {
		header, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading cbr entry: %w", err)
		}
		if header.IsDir {
			continue
		}
		entries = append(entries, Entry{
			Name: header.Name,
			Size: header.UnPackedSize,
		})
	}
	return entries, nil
}

func (a *cbrArchive) ExtractFile(name string) (io.ReadCloser, error) {
	r, err := rardecode.OpenReader(a.path)
	if err != nil {
		return nil, fmt.Errorf("reading cbr: %w", err)
	}

	for {
		header, err := r.Next()
		if err == io.EOF {
			r.Close()
			return nil, fmt.Errorf("file not found in archive: %s", name)
		}
		if err != nil {
			r.Close()
			return nil, fmt.Errorf("reading cbr entry: %w", err)
		}
		if header.Name == name {
			// Read the entire file into memory since RAR requires sequential reading
			data, err := io.ReadAll(r)
			r.Close()
			if err != nil {
				return nil, fmt.Errorf("extracting cbr file: %w", err)
			}
			return io.NopCloser(bytes.NewReader(data)), nil
		}
	}
}

func (a *cbrArchive) Close() error {
	return nil
}
