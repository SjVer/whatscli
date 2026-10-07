package messages

import (
	"path/filepath"

	"github.com/normen/whatscli/config"
)

// The AI model, which describes images, see describeChat, and answers
// /recap and /ask, runs in package ai, from the llama folder next to the
// config, see AiModel.

// modelNotice is the key of the notices about the AI model, on the main screen
const modelNotice = "model"

// modelDir returns the folder that keeps llama.cpp, the models and ffmpeg,
// "" without a config folder
func modelDir() string {
	if configPath := config.GetConfigFilePath(); configPath != "" {
		return filepath.Join(filepath.Dir(configPath), "llama")
	}
	return ""
}

// showModelNotice shows what the AI model is doing on the main screen
func (sm *SessionManager) showModelNotice(text string) {
	sm.uiHandler.SetNotice("", modelNotice, text)
}

// ensureModel starts the AI model if it doesn't run yet, see ai.Server.Ensure,
// and returns its URL
func (sm *SessionManager) ensureModel() (string, error) {
	return sm.model.Ensure(config.Config.General.AiModel, modelDir(), sm.showModelNotice, sm.Log)
}

// ensureFFmpeg returns the path of ffmpeg, see ai.FFmpeg.Ensure
func (sm *SessionManager) ensureFFmpeg() (string, error) {
	dir := modelDir()
	if dir != "" {
		dir = filepath.Join(dir, "ffmpeg")
	}
	return sm.ffmpeg.Ensure(dir, sm.showModelNotice, sm.Log)
}
