package factory

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func writeArchive(source, destination, rootName string) error {
	file, err := os.CreateTemp(filepath.Dir(destination), ".reaper-archive-")
	if err != nil {
		return fmt.Errorf("create archive: %w", err)
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	writer := zip.NewWriter(file)
	walkErr := filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		archivePath := filepath.ToSlash(filepath.Join(rootName, relative))
		destinationFile, err := writer.Create(archivePath)
		if err != nil {
			return err
		}
		sourceFile, err := os.Open(path) //nolint:gosec // WalkDir produced path from the selected generation root.
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(destinationFile, sourceFile)
		closeErr := sourceFile.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	closeWriterErr := writer.Close()
	closeFileErr := file.Close()
	if walkErr != nil {
		return fmt.Errorf("archive generated repository: %w", walkErr)
	}
	if closeWriterErr != nil {
		return fmt.Errorf("finish archive: %w", closeWriterErr)
	}
	if closeFileErr != nil {
		return fmt.Errorf("close archive: %w", closeFileErr)
	}
	if err := os.Link(temporary, destination); err != nil {
		return fmt.Errorf("publish archive without overwrite: %w", err)
	}
	return nil
}
