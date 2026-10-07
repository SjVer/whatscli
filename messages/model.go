package messages

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

	"github.com/normen/whatscli/config"
)

// The AI model, which describes images, see describeChat, and answers
// /recap and /ask, runs in llama.cpp's llama-server, which whatscli downloads
// once and starts when it is first needed. The server downloads the model
// itself from Hugging Face, see AiModel, and answers like the OpenAI chat API,
// see askModel.

// modelNotice is the key of the notices about the AI model, on the main screen
const modelNotice = "model"

// llamaBuild is the release of llama.cpp that is downloaded
const llamaBuild = "b11146"

// serverStartTimeout is how long the server may take to start, which includes
// downloading the model the first time
const serverStartTimeout = 30 * time.Minute

// modelServer is the running llama-server
type modelServer struct {
	lock sync.Mutex
	url  string
	err  error
	// closed when the server exits, after which it is started again
	exited chan struct{}
	// the process, apart from lock, which is held while it starts, see stopModel
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

// ensureModel starts llama-server if it doesn't run yet, downloading it the
// first time, and returns its URL. A failure is remembered, so that it isn't
// tried again for every image or question.
func (sm *SessionManager) ensureModel() (string, error) {
	sm.model.lock.Lock()
	defer sm.model.lock.Unlock()
	if sm.model.url != "" {
		select {
		case <-sm.model.exited:
			sm.logWarn("llama-server stopped, starting it again")
			sm.model.url = ""
		default:
			return sm.model.url, nil
		}
	}
	if sm.model.err != nil {
		return "", sm.model.err
	}
	sm.uiHandler.SetNotice("", modelNotice, "Starting the AI model, the first time it is downloaded, which can take a while...")
	sm.model.url, sm.model.err = sm.startModel()
	if sm.model.err != nil {
		sm.uiHandler.SetNotice("", modelNotice, "The AI model isn't available: "+sm.model.err.Error())
	} else {
		sm.uiHandler.SetNotice("", modelNotice, "")
	}
	return sm.model.url, sm.model.err
}

func (sm *SessionManager) startModel() (string, error) {
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

	cmd := exec.Command(binary, "-hf", config.Config.General.AiModel, "--host", "127.0.0.1", "--port", fmt.Sprint(port), "-c", "8192")
	cmd.Env = append(os.Environ(), "LLAMA_CACHE="+filepath.Join(dir, "models"))
	output, _ := cmd.StdoutPipe()
	cmd.Stderr = cmd.Stdout
	sm.model.cmdLock.Lock()
	if sm.model.stopped {
		sm.model.cmdLock.Unlock()
		return "", errors.New("whatscli is closing")
	}
	err = startServerProcess(cmd)
	sm.model.cmd = cmd
	sm.model.cmdLock.Unlock()
	if err != nil {
		return "", err
	}
	stopped := make(chan struct{})
	sm.model.exited = stopped
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

// stopModel stops llama-server when whatscli closes, also while it starts
func (sm *SessionManager) stopModel() {
	sm.model.cmdLock.Lock()
	defer sm.model.cmdLock.Unlock()
	sm.model.stopped = true
	if sm.model.cmd != nil && sm.model.cmd.Process != nil {
		sm.model.cmd.Process.Kill()
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

// askModel asks the model at url, with images of the type if there are any,
// for an answer of at most maxTokens, the way the OpenAI chat API is asked,
// which llama-server answers like
func askModel(url, prompt string, maxTokens int, mimeType string, images ...[]byte) (string, error) {
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
