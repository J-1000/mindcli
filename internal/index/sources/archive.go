package sources

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const maxArchiveEntries = 10000

// archiveBudget bounds both the owning file and the sum of uncompressed entry
// bytes. Parsers consume it without extracting archive paths to disk.
type archiveBudget struct {
	remaining int64
	entries   int
}

func newArchiveBudget(maxDecompressedBytes int64) *archiveBudget {
	return &archiveBudget{remaining: maxDecompressedBytes}
}

func readFileBounded(path string, maxBytes int64) ([]byte, error) {
	if maxBytes < 1 {
		return nil, fmt.Errorf("invalid file byte limit %d", maxBytes)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", path)
	}
	if info.Size() > maxBytes {
		return nil, fmt.Errorf("file %s is %d bytes; limit is %d", filepath.Base(path), info.Size(), maxBytes)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	data, readErr := readBounded(f, maxBytes)
	closeErr := f.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return data, nil
}

func readBounded(reader io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes < 1 {
		return nil, fmt.Errorf("invalid byte limit %d", maxBytes)
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("content exceeds %d-byte limit", maxBytes)
	}
	return data, nil
}

func openBoundedZIP(data []byte, maxDecompressedBytes int64) (*zip.Reader, *archiveBudget, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, nil, err
	}
	if len(reader.File) > maxArchiveEntries {
		return nil, nil, fmt.Errorf("archive has %d entries; limit is %d", len(reader.File), maxArchiveEntries)
	}
	return reader, newArchiveBudget(maxDecompressedBytes), nil
}

func readZIPEntry(file *zip.File, budget *archiveBudget) ([]byte, error) {
	if file == nil || budget == nil {
		return nil, fmt.Errorf("invalid archive entry")
	}
	budget.entries++
	if budget.entries > maxArchiveEntries {
		return nil, fmt.Errorf("archive exceeds %d-entry limit", maxArchiveEntries)
	}
	if file.FileInfo().IsDir() {
		return nil, nil
	}
	if file.UncompressedSize64 > uint64(budget.remaining) {
		return nil, fmt.Errorf("archive entry %q exceeds remaining %d-byte expansion budget", file.Name, budget.remaining)
	}
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	data, readErr := readBounded(reader, budget.remaining)
	closeErr := reader.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	budget.remaining -= int64(len(data))
	return data, nil
}

func findZIPEntry(reader *zip.Reader, name string) *zip.File {
	name = strings.TrimPrefix(filepath.ToSlash(name), "/")
	for _, file := range reader.File {
		if strings.EqualFold(strings.TrimPrefix(filepath.ToSlash(file.Name), "/"), name) {
			return file
		}
	}
	return nil
}
