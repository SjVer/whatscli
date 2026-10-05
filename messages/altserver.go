package messages

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/normen/whatscli/config"
)

// The model that describes images runs in llama.cpp's llama-server, which
// whatscli downloads once and starts when the first image is described. The
// server downloads the model itself from Hugging Face, see AltTextModel, and
// answers like the OpenAI chat API, see describe.

// llamaBuild is the release of llama.cpp that is downloaded
const llamaBuild = "b11146"

// serverStartTimeout is how long the server may take to start, which includes
// downloading the model the first time
const serverStartTimeout = 30 * time.Minute

// altServer is the running llama-server
type altServer struct {
	lock sync.Mutex
	url  string
	err  error
	// closed when the server exits, after which it is started again
	exited chan struct{}
	// the process, apart from lock, which is held while it starts, see stopServer
	cmdLock sync.Mutex
	cmd     *exec.Cmd
	stopped bool
}

// llamaArchive returns the name of the llama.cpp release file for a platform,
// or "" if there is none. Vulkan runs on GPUs of all makers without CUDA.
func llamaArchive(goos, goarch string) string {
	name := map[string]string{
		"windows/amd64": "bin-win-vulkan-x64.zip",
		"linux/amd64":   "bin-ubuntu-vulkan-x64.tar.gz",
		"darwin/arm64":  "bin-macos-arm64.tar.gz",
		"darwin/amd64":  "bin-macos-x64.tar.gz",
	}[goos+"/"+goarch]
	if name == "" {
		return ""
	}
	return "llama-" + llamaBuild + "-" + name
}

// ensureServer starts llama-server if it doesn't run yet, downloading it the
// first time, and returns its URL. A failure is remembered, so that it isn't
// tried again for every image.
func (sm *SessionManager) ensureServer() (string, error) {
	sm.altServer.lock.Lock()
	defer sm.altServer.lock.Unlock()
	if sm.altServer.url != "" {
		select {
		case <-sm.altServer.exited:
			sm.logWarn("llama-server stopped, starting it again")
			sm.altServer.url = ""
		default:
			return sm.altServer.url, nil
		}
	}
	if sm.altServer.err != nil {
		return "", sm.altServer.err
	}
	sm.uiHandler.SetNotice("", altTextNotice, "Starting the model for alt texts, the first time it is downloaded, which can take a while...")
	sm.altServer.url, sm.altServer.err = sm.startServer()
	if sm.altServer.err != nil {
		sm.uiHandler.SetNotice("", altTextNotice, "Alt texts aren't available: "+sm.altServer.err.Error())
	} else {
		sm.uiHandler.SetNotice("", altTextNotice, "")
	}
	return sm.altServer.url, sm.altServer.err
}

func (sm *SessionManager) startServer() (string, error) {
	configPath := config.GetConfigFilePath()
	if configPath == "" {
		return "", errors.New("there is no config folder to keep the model in")
	}
	dir := filepath.Join(filepath.Dir(configPath), "llama")
	binary, err := downloadServer(filepath.Join(dir, llamaBuild))
	if err != nil {
		return "", err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0") // a free port
	if err != nil {
		return "", err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	cmd := exec.Command(binary, "-hf", config.Config.General.AltTextModel, "--host", "127.0.0.1", "--port", fmt.Sprint(port), "-c", "4096")
	cmd.Env = append(os.Environ(), "LLAMA_CACHE="+filepath.Join(dir, "models"))
	output, _ := cmd.StdoutPipe()
	cmd.Stderr = cmd.Stdout
	sm.altServer.cmdLock.Lock()
	if sm.altServer.stopped {
		sm.altServer.cmdLock.Unlock()
		return "", errors.New("whatscli is closing")
	}
	err = startServerProcess(cmd)
	sm.altServer.cmd = cmd
	sm.altServer.cmdLock.Unlock()
	if err != nil {
		return "", err
	}
	stopped := make(chan struct{})
	sm.altServer.exited = stopped
	go func() {
		// its progress, e.g. of downloading the model
		for lines := bufio.NewScanner(output); lines.Scan(); {
			sm.logDebug("llama-server: %s", lines.Text())
		}
		cmd.Wait()
		close(stopped)
	}()
	url := fmt.Sprintf("http://127.0.0.1:%d", port)
	for start := time.Now(); time.Since(start) < serverStartTimeout; time.Sleep(time.Second) {
		select {
		case <-stopped:
			return "", errors.New("llama-server stopped, see the log")
		default:
		}
		if resp, err := healthClient.Get(url + "/health"); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return url, nil
			}
		}
	}
	cmd.Process.Kill()
	return "", errors.New("llama-server didn't start in time")
}

// healthClient asks the server whether it runs
var healthClient = http.Client{Timeout: 5 * time.Second}

// stopServer stops llama-server when whatscli closes, also while it starts
func (sm *SessionManager) stopServer() {
	sm.altServer.cmdLock.Lock()
	defer sm.altServer.cmdLock.Unlock()
	sm.altServer.stopped = true
	if sm.altServer.cmd != nil && sm.altServer.cmd.Process != nil {
		sm.altServer.cmd.Process.Kill()
	}
}

// downloadServer downloads and unpacks llama.cpp into dir if it isn't there,
// and returns the path of llama-server
func downloadServer(dir string) (string, error) {
	if binary := findServer(dir); binary != "" {
		return binary, nil
	}
	archive := llamaArchive(runtime.GOOS, runtime.GOARCH)
	if archive == "" {
		return "", fmt.Errorf("llama.cpp has no release for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	download := http.Client{Timeout: 30 * time.Minute}
	resp, err := download.Get("https://github.com/ggml-org/llama.cpp/releases/download/" + llamaBuild + "/" + archive)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("downloading llama.cpp failed: %s", resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	// unpacked next to it first, so that an unpack that was cut off isn't used
	tmp := dir + ".tmp"
	os.RemoveAll(tmp)
	if err = unpack(data, strings.HasSuffix(archive, ".zip"), tmp); err != nil {
		os.RemoveAll(tmp)
		return "", err
	}
	if err = os.Rename(tmp, dir); err != nil {
		return "", err
	}
	if binary := findServer(dir); binary != "" {
		return binary, nil
	}
	return "", errors.New("llama-server isn't in the llama.cpp release")
}

// findServer returns the path of llama-server in dir, or ""
func findServer(dir string) string {
	name := "llama-server"
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
