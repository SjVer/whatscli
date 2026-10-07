package ai

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Package ai runs the local AI model of whatscli, which describes images and
// answers questions about chats, and prepares what it is shown, like the
// frames of videos.
//
// The model runs in llama.cpp's llama-server, which is downloaded once and
// started when it is first needed, see Server. The server downloads the model
// itself from Hugging Face, and answers like the OpenAI chat API, see Ask.

// llamaBuild is the release of llama.cpp that is downloaded
const llamaBuild = "b11146"

// serverStartTimeout is how long the server may take to start, which includes
// downloading the model the first time
const serverStartTimeout = 30 * time.Minute

// Logger receives what happens, like the output of llama-server
type Logger interface {
	Debugf(msg string, args ...any)
	Warnf(msg string, args ...any)
}

// Server is the llama-server that runs the model, started by Ensure
type Server struct {
	lock sync.Mutex
	url  string
	err  error
	// closed when the server exits, after which it is started again
	exited chan struct{}
	// the process, apart from lock, which is held while it starts, see Stop
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

// Ensure starts llama-server with the model, a Hugging Face repo, if it
// doesn't run yet, downloading both into dir the first time, and returns its
// URL; it can't without a dir. notice tells what happens while it starts, ""
// when that is over. A failure is remembered, so that it isn't tried again
// for every image or question.
func (s *Server) Ensure(model, dir string, notice func(string), log Logger) (string, error) {
	s.lock.Lock()
	defer s.lock.Unlock()
	if s.url != "" {
		select {
		case <-s.exited:
			logf(log, "llama-server stopped, starting it again")
			s.url = ""
		default:
			return s.url, nil
		}
	}
	if s.err != nil {
		return "", s.err
	}
	notice("Starting the AI model, the first time it is downloaded, which can take a while...")
	s.url, s.err = s.start(model, dir, log)
	if s.err != nil {
		notice("The AI model isn't available: " + s.err.Error())
	} else {
		notice("")
	}
	return s.url, s.err
}

// SetURL makes the server answer at url without starting it, for tests
func (s *Server) SetURL(url string) {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.url = url
}

func (s *Server) start(model, dir string, log Logger) (string, error) {
	if dir == "" {
		return "", errors.New("there is no config folder to keep the model in")
	}
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

	cmd := exec.Command(binary, "-hf", model, "--host", "127.0.0.1", "--port", fmt.Sprint(port), "-c", "8192")
	cmd.Env = append(os.Environ(), "LLAMA_CACHE="+filepath.Join(dir, "models"))
	output, _ := cmd.StdoutPipe()
	cmd.Stderr = cmd.Stdout
	s.cmdLock.Lock()
	if s.stopped {
		s.cmdLock.Unlock()
		return "", errors.New("whatscli is closing")
	}
	err = startServerProcess(cmd)
	s.cmd = cmd
	s.cmdLock.Unlock()
	if err != nil {
		return "", err
	}
	stopped := make(chan struct{})
	s.exited = stopped
	go func() {
		// its progress, e.g. of downloading the model
		for lines := bufio.NewScanner(output); lines.Scan(); {
			if log != nil {
				log.Debugf("llama-server: %s", lines.Text())
			}
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

// logf writes a warning to log, if there is one
func logf(log Logger, format string, args ...any) {
	if log != nil {
		log.Warnf(format, args...)
	}
}

// healthClient asks the server whether it runs
var healthClient = http.Client{Timeout: 5 * time.Second}

// Stop stops llama-server when whatscli closes, also while it starts, and
// keeps it from starting again
func (s *Server) Stop() {
	s.cmdLock.Lock()
	defer s.cmdLock.Unlock()
	s.stopped = true
	if s.cmd != nil && s.cmd.Process != nil {
		s.cmd.Process.Kill()
	}
}

// downloadServer downloads and unpacks llama.cpp into dir if it isn't there,
// and returns the path of llama-server
func downloadServer(dir string) (string, error) {
	archive := llamaArchive(runtime.GOOS, runtime.GOARCH)
	if archive == "" && findBinary(dir, "llama-server") == "" {
		return "", fmt.Errorf("llama.cpp has no release for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	return downloadTool(dir, "https://github.com/ggml-org/llama.cpp/releases/download/"+llamaBuild+"/"+archive, "llama-server")
}

// Ask asks the model at url, with images of the type if there are any,
// for an answer of at most maxTokens, the way the OpenAI chat API is asked,
// which llama-server answers like
func Ask(url, prompt string, maxTokens int, mimeType string, images ...[]byte) (string, error) {
	content := []map[string]any{{"type": "text", "text": prompt}}
	for _, data := range images {
		content = append(content, map[string]any{"type": "image_url",
			"image_url": map[string]string{"url": "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)}})
	}
	request, _ := json.Marshal(map[string]any{
		"max_tokens": maxTokens,
		"messages":   []map[string]any{{"role": "user", "content": content}},
	})
	client := http.Client{Timeout: 5 * time.Minute} // answering about many messages takes a while
	resp, err := client.Post(url+"/v1/chat/completions", "application/json", bytes.NewReader(request))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the model answered %s", resp.Status)
	}
	var answer struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&answer); err != nil {
		return "", err
	}
	if len(answer.Choices) == 0 {
		return "", errors.New("the model gave no answer")
	}
	return strings.TrimSpace(answer.Choices[0].Message.Content), nil
}
