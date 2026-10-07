package ai

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLlamaArchives(t *testing.T) {
	if name := llamaArchive("windows", "amd64"); name != "llama-"+llamaBuild+"-bin-win-vulkan-x64.zip" {
		t.Errorf("unexpected archive for Windows: %q", name)
	}
	if name := llamaArchive("darwin", "arm64"); !strings.HasSuffix(name, "-bin-macos-arm64.tar.gz") {
		t.Errorf("unexpected archive for macOS: %q", name)
	}
	if name := llamaArchive("plan9", "386"); name != "" {
		t.Errorf("expected no archive, got %q", name)
	}
}

func TestUnpackingTheServer(t *testing.T) {
	var zipped bytes.Buffer
	archive := zip.NewWriter(&zipped)
	file, _ := archive.Create("build/bin/llama-server.exe")
	file.Write([]byte("server"))
	archive.Close()
	dir := t.TempDir()
	if err := unpack(zipped.Bytes(), true, dir); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "build", "bin", "llama-server.exe")); err != nil || string(data) != "server" {
		t.Errorf("expected the server to be unpacked, got %q, %v", data, err)
	}

	var tarred bytes.Buffer
	compressed := gzip.NewWriter(&tarred)
	files := tar.NewWriter(compressed)
	files.WriteHeader(&tar.Header{Name: "llama/llama-server", Mode: 0755, Size: 6, Typeflag: tar.TypeReg})
	files.Write([]byte("server"))
	files.WriteHeader(&tar.Header{Name: "../outside", Mode: 0644, Size: 1, Typeflag: tar.TypeReg})
	files.Write([]byte("x"))
	files.Close()
	compressed.Close()
	if err := unpack(tarred.Bytes(), false, t.TempDir()); err == nil {
		t.Error("expected a path outside the folder to be refused")
	}
}
