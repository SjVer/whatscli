package ai

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// downloadTool, findBinary and unpack install the programs that run the model
// and prepare what it is shown: llama-server, see downloadServer, and ffmpeg.

// downloadTool downloads and unpacks the zip or tar.gz archive at url into dir
// if it isn't there, and returns the path of the program name in it
func downloadTool(dir, url, name string) (string, error) {
	if binary := findBinary(dir, name); binary != "" {
		return binary, nil
	}
	download := http.Client{Timeout: 30 * time.Minute}
	resp, err := download.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("downloading %s failed: %s", name, resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	// unpacked next to it first, so that an unpack that was cut off isn't used
	tmp := dir + ".tmp"
	os.RemoveAll(tmp)
	if err = unpack(data, strings.HasSuffix(url, ".zip"), tmp); err != nil {
		os.RemoveAll(tmp)
		return "", err
	}
	if err = os.Rename(tmp, dir); err != nil {
		return "", err
	}
	if binary := findBinary(dir, name); binary != "" {
		return binary, nil
	}
	return "", fmt.Errorf("%s isn't in the downloaded archive", name)
}

// findBinary returns the path of the program name in dir, or ""
func findBinary(dir, name string) string {
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	found := ""
	filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && entry.Name() == name {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// unpack writes the files of a zip or tar.gz archive into dir
func unpack(data []byte, isZip bool, dir string) error {
	// inside returns the path of a file of the archive, if it is in dir
	inside := func(name string) (string, error) {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if !strings.HasPrefix(path, filepath.Clean(dir)+string(os.PathSeparator)) {
			return "", fmt.Errorf("unsafe path in the archive: %s", name)
		}
		return path, nil
	}
	write := func(name string, mode os.FileMode, content io.Reader) error {
		path, err := inside(name)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode|0600)
		if err != nil {
			return err
		}
		defer file.Close()
		_, err = io.Copy(file, content)
		return err
	}
	if isZip {
		archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return err
		}
		for _, entry := range archive.File {
			if entry.FileInfo().IsDir() {
				continue
			}
			content, err := entry.Open()
			if err != nil {
				return err
			}
			err = write(entry.Name, entry.Mode(), content)
			content.Close()
			if err != nil {
				return err
			}
		}
		return nil
	}
	compressed, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	archive := tar.NewReader(compressed)
	for {
		entry, err := archive.Next()
		if err == io.EOF {
			return nil
		} else if err != nil {
			return err
		}
		switch entry.Typeflag {
		case tar.TypeReg:
			if err = write(entry.Name, entry.FileInfo().Mode(), archive); err != nil {
				return err
			}
		case tar.TypeSymlink:
			// libraries on macOS and Linux, by their version, next to them
			path, err := inside(entry.Name)
			if err != nil {
				return err
			} else if filepath.IsAbs(entry.Linkname) || strings.Contains(entry.Linkname, "..") {
				return fmt.Errorf("unsafe link in the archive: %s", entry.Name)
			}
			os.MkdirAll(filepath.Dir(path), 0755)
			os.Remove(path)
			if err = os.Symlink(entry.Linkname, path); err != nil {
				return err
			}
		}
	}
}
