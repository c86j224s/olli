package tools

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	urlpath "path"
	"path/filepath"
	"strings"
	"time"

	"github.com/c86j224s/olli/ollama"
	"golang.org/x/sys/unix"
)

const (
	audioGenerateToolName                  = "audio_generate"
	aceStepBackendAlias                    = "ace_step"
	defaultACEStepEndpoint                 = "http://127.0.0.1:8001"
	defaultAudioOutputDir                  = "artifacts/audio"
	defaultAudioTimeoutSeconds             = 900
	defaultAudioMaxAudioBytes        int64 = 209715200
	defaultACEStepPollIntervalMS           = 1000
	defaultACEStepMaxDurationSeconds       = 600
	maxAudioTimeoutSeconds                 = 3600
	maxAudioMaxAudioBytes            int64 = defaultAudioMaxAudioBytes
	maxACEStepJSONResponseBytes      int64 = 8388608
	minACEStepPollIntervalMS               = 100
	maxACEStepPollIntervalMS               = 60000
	maxACEStepDurationSeconds              = 600
)

type AudioGenerationConfig struct {
	ACEStep ACEStepConfig
}

type ACEStepConfig struct {
	Endpoint           string
	OutputDir          string
	TimeoutSeconds     int
	MaxAudioBytes      int64
	PollIntervalMS     int
	MaxDurationSeconds int
}

type aceStepEnvelope struct {
	Data  json.RawMessage `json:"data"`
	Code  int             `json:"code"`
	Error *string         `json:"error"`
}

type aceStepQueryItem struct {
	TaskID       string `json:"task_id"`
	Status       int    `json:"status"`
	Result       string `json:"result"`
	ProgressText string `json:"progress_text"`
}

type aceStepGeneratedAudio struct {
	File   string                 `json:"file"`
	Status int                    `json:"status"`
	Prompt string                 `json:"prompt"`
	Lyrics string                 `json:"lyrics"`
	Metas  map[string]interface{} `json:"metas"`
}

type audioGenerateResult struct {
	BackendAlias          string `json:"backend_alias"`
	TaskID                string `json:"task_id"`
	ArtifactPath          string `json:"artifact_path"`
	WorkspaceRelativePath string `json:"workspace_relative_path"`
	Bytes                 int    `json:"bytes"`
	RemoteFile            string `json:"remote_file"`
}

func DefaultAudioGenerationConfig() AudioGenerationConfig {
	return AudioGenerationConfig{
		ACEStep: ACEStepConfig{
			Endpoint:           defaultACEStepEndpoint,
			OutputDir:          defaultAudioOutputDir,
			TimeoutSeconds:     defaultAudioTimeoutSeconds,
			MaxAudioBytes:      defaultAudioMaxAudioBytes,
			PollIntervalMS:     defaultACEStepPollIntervalMS,
			MaxDurationSeconds: defaultACEStepMaxDurationSeconds,
		},
	}
}

func (c AudioGenerationConfig) withDefaults() AudioGenerationConfig {
	defaults := DefaultAudioGenerationConfig()
	if c.ACEStep.Endpoint == "" {
		c.ACEStep.Endpoint = defaults.ACEStep.Endpoint
	}
	if c.ACEStep.OutputDir == "" {
		c.ACEStep.OutputDir = defaults.ACEStep.OutputDir
	}
	if c.ACEStep.TimeoutSeconds <= 0 {
		c.ACEStep.TimeoutSeconds = defaults.ACEStep.TimeoutSeconds
	}
	if c.ACEStep.MaxAudioBytes <= 0 {
		c.ACEStep.MaxAudioBytes = defaults.ACEStep.MaxAudioBytes
	}
	if c.ACEStep.PollIntervalMS <= 0 {
		c.ACEStep.PollIntervalMS = defaults.ACEStep.PollIntervalMS
	}
	if c.ACEStep.MaxDurationSeconds <= 0 {
		c.ACEStep.MaxDurationSeconds = defaults.ACEStep.MaxDurationSeconds
	}
	return c
}

func (r *Registry) SetAudioGenerationConfig(cfg AudioGenerationConfig) {
	r.audioGeneration = cfg.withDefaults()
}

func validateAudioGenerationRuntimeConfig(cfg AudioGenerationConfig) error {
	if cfg.ACEStep.TimeoutSeconds <= 0 || cfg.ACEStep.TimeoutSeconds > maxAudioTimeoutSeconds {
		return fmt.Errorf("audio generation config error: timeout_seconds must be between 1 and %d", maxAudioTimeoutSeconds)
	}
	if cfg.ACEStep.MaxAudioBytes <= 0 || cfg.ACEStep.MaxAudioBytes > maxAudioMaxAudioBytes {
		return fmt.Errorf("audio generation config error: max_audio_bytes must be between 1 and %d", maxAudioMaxAudioBytes)
	}
	if cfg.ACEStep.PollIntervalMS < minACEStepPollIntervalMS || cfg.ACEStep.PollIntervalMS > maxACEStepPollIntervalMS {
		return fmt.Errorf("audio generation config error: poll_interval_ms must be between %d and %d", minACEStepPollIntervalMS, maxACEStepPollIntervalMS)
	}
	if cfg.ACEStep.MaxDurationSeconds <= 0 || cfg.ACEStep.MaxDurationSeconds > maxACEStepDurationSeconds {
		return fmt.Errorf("audio generation config error: max_duration_seconds must be between 1 and %d", maxACEStepDurationSeconds)
	}
	return nil
}

func (r *Registry) registerAudioGenerateTool() {
	r.RegisterContext(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        audioGenerateToolName,
			Description: "Generate music through a configured local ACE-Step API server and save the first WAV output as a workspace artifact",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"backend_alias": {
						Type:        "string",
						Description: "Configured audio backend alias. Only 'ace_step' is supported.",
						Enum:        []string{aceStepBackendAlias},
					},
					"prompt": {
						Type:        "string",
						Description: "Music description prompt",
					},
					"lyrics": {
						Type:        "string",
						Description: "Optional lyrics content",
					},
					"duration_seconds": {
						Type:        "integer",
						Description: "Optional generation duration in seconds, bounded by audio_generation.ace_step.max_duration_seconds",
					},
					"seed": {
						Type:        "integer",
						Description: "Optional non-negative seed. When omitted, ACE-Step uses a random seed.",
					},
					"model": {
						Type:        "string",
						Description: "Optional ACE-Step DiT model name configured on the server",
					},
					"thinking": {
						Type:        "boolean",
						Description: "Optional ACE-Step thinking flag for 5Hz LM-assisted generation",
					},
					"vocal_language": {
						Type:        "string",
						Description: "Optional lyrics language code, such as en, ko, zh, or ja",
					},
				},
				Required: []string{"backend_alias", "prompt"},
			},
		},
	}, ToolMetadata{WorkflowCallable: true}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		return r.executeAudioGenerate(ctx, args)
	})
}

func (r *Registry) executeAudioGenerate(ctx context.Context, args map[string]interface{}) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	cfg := r.audioGeneration.withDefaults()
	if err := validateAudioGenerationRuntimeConfig(cfg); err != nil {
		return "", err
	}

	backendAlias, err := requiredStringArg(args, "backend_alias")
	if err != nil {
		return "", err
	}
	if backendAlias != aceStepBackendAlias {
		return "", fmt.Errorf("audio generation config error: backend_alias %q is not configured; allowed backend: %s", backendAlias, aceStepBackendAlias)
	}

	prompt, err := requiredStringArg(args, "prompt")
	if err != nil {
		return "", err
	}
	lyrics, hasLyrics, err := optionalStringArg(args, "lyrics")
	if err != nil {
		return "", err
	}
	durationSeconds, hasDuration, err := optionalBoundedPositiveIntArg(args, "duration_seconds", cfg.ACEStep.MaxDurationSeconds)
	if err != nil {
		return "", err
	}
	seed, hasSeed, err := optionalNonNegativeInt64Arg(args, "seed")
	if err != nil {
		return "", err
	}
	model, hasModel, err := optionalNonBlankStringArg(args, "model")
	if err != nil {
		return "", err
	}
	vocalLanguage, hasVocalLanguage, err := optionalNonBlankStringArg(args, "vocal_language")
	if err != nil {
		return "", err
	}
	thinking, hasThinking, err := optionalBoolArg(args, "thinking")
	if err != nil {
		return "", err
	}

	endpoint, err := validateACEStepEndpoint(cfg.ACEStep.Endpoint)
	if err != nil {
		return "", err
	}

	root, err := IsPathSafeFrom(".", r.GetWorkspaceRoot(), r.GetWorkspaceRoot())
	if err != nil {
		return "", fmt.Errorf("audio generation config error: workspace root rejected: %w", err)
	}
	if _, err := ensureAudioArtifactOutputDir(cfg.ACEStep.OutputDir, root); err != nil {
		return "", err
	}

	payload := map[string]interface{}{
		"prompt":          prompt,
		"audio_format":    "wav",
		"batch_size":      1,
		"task_type":       "text2music",
		"use_random_seed": true,
	}
	if hasLyrics {
		payload["lyrics"] = lyrics
	}
	if hasDuration {
		payload["audio_duration"] = durationSeconds
	}
	if hasSeed {
		payload["use_random_seed"] = false
		payload["seed"] = seed
	}
	if hasModel {
		payload["model"] = model
	}
	if hasThinking {
		payload["thinking"] = thinking
	}
	if hasVocalLanguage {
		payload["vocal_language"] = vocalLanguage
	}

	timeout := time.Duration(cfg.ACEStep.TimeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	client := newLocalACEStepHTTPClient(timeout)
	taskID, err := submitACEStepTask(ctx, client, endpoint, payload)
	if err != nil {
		return "", err
	}
	pollInterval := time.Duration(cfg.ACEStep.PollIntervalMS) * time.Millisecond
	generated, err := pollACEStepTask(ctx, client, endpoint, taskID, pollInterval)
	if err != nil {
		return "", err
	}
	audioBytes, err := downloadACEStepAudio(ctx, client, endpoint, generated.File, cfg.ACEStep.MaxAudioBytes)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	artifactPath, err := writeAudioArtifact(ctx, cfg.ACEStep.OutputDir, root, taskID, generated.File, audioBytes)
	if err != nil {
		return "", err
	}

	relPath, relErr := filepath.Rel(root, artifactPath)
	if relErr != nil {
		relPath = artifactPath
	}
	result := audioGenerateResult{
		BackendAlias:          backendAlias,
		TaskID:                taskID,
		ArtifactPath:          artifactPath,
		WorkspaceRelativePath: relPath,
		Bytes:                 len(audioBytes),
		RemoteFile:            generated.File,
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal audio generation result: %w", err)
	}
	return string(data), nil
}

func optionalBoolArg(args map[string]interface{}, key string) (bool, bool, error) {
	value, ok := args[key]
	if !ok || value == nil {
		return false, false, nil
	}
	boolValue, ok := value.(bool)
	if !ok {
		return false, false, fmt.Errorf("invalid %s argument: must be a boolean", key)
	}
	return boolValue, true, nil
}

func optionalNonBlankStringArg(args map[string]interface{}, key string) (string, bool, error) {
	value, ok := args[key]
	if !ok || value == nil {
		return "", false, nil
	}
	str, ok := value.(string)
	if !ok {
		return "", false, fmt.Errorf("invalid %s argument", key)
	}
	str = strings.TrimSpace(str)
	if str == "" {
		return "", false, fmt.Errorf("invalid %s argument", key)
	}
	return str, true, nil
}

func validateACEStepEndpoint(rawEndpoint string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawEndpoint))
	if err != nil {
		return nil, fmt.Errorf("audio generation config error: invalid ACE-Step endpoint: %w", err)
	}
	if parsed.Scheme != "http" {
		return nil, fmt.Errorf("audio generation config error: ACE-Step endpoint scheme must be http")
	}
	if parsed.User != nil {
		return nil, fmt.Errorf("audio generation config error: ACE-Step endpoint must not include userinfo")
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("audio generation config error: ACE-Step endpoint host is required")
	}
	if parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("audio generation config error: ACE-Step endpoint must not include path, query, or fragment")
	}
	switch parsed.Hostname() {
	case "127.0.0.1", "::1", "localhost":
	default:
		return nil, fmt.Errorf("audio generation config error: ACE-Step endpoint host must be loopback/local")
	}
	return parsed, nil
}

func newLocalACEStepHTTPClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout:   timeout,
		KeepAlive: 30 * time.Second,
	}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network string, address string) (net.Conn, error) {
			conn, err := dialer.DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			tcpAddr, ok := conn.RemoteAddr().(*net.TCPAddr)
			if !ok || !tcpAddr.IP.IsLoopback() {
				_ = conn.Close()
				return nil, fmt.Errorf("audio generation security block: ACE-Step connection resolved outside loopback")
			}
			return conn, nil
		},
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func submitACEStepTask(ctx context.Context, client *http.Client, endpoint *url.URL, payload map[string]interface{}) (string, error) {
	reqBody, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to encode ACE-Step task request: %w", err)
	}

	releaseURL := *endpoint
	releaseURL.Path = "/release_task"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, releaseURL.String(), bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("failed to create ACE-Step task request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to submit ACE-Step task: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ACE-Step task request returned status %d: %s", resp.StatusCode, readLimitedErrorBody(resp.Body))
	}

	var envelope aceStepEnvelope
	if err := decodeLimitedJSON(resp.Body, maxACEStepJSONResponseBytes, "ACE-Step task response", &envelope); err != nil {
		return "", fmt.Errorf("failed to decode ACE-Step task response: %w", err)
	}
	if err := validateACEStepEnvelope(envelope, "task"); err != nil {
		return "", err
	}
	var data struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		return "", fmt.Errorf("failed to decode ACE-Step task data: %w", err)
	}
	if strings.TrimSpace(data.TaskID) == "" {
		return "", fmt.Errorf("ACE-Step task response did not include task_id")
	}
	return data.TaskID, nil
}

func pollACEStepTask(ctx context.Context, client *http.Client, endpoint *url.URL, taskID string, interval time.Duration) (aceStepGeneratedAudio, error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		generated, done, err := fetchACEStepTaskResult(ctx, client, endpoint, taskID)
		if err != nil {
			return aceStepGeneratedAudio{}, err
		}
		if done {
			return generated, nil
		}

		select {
		case <-ctx.Done():
			return aceStepGeneratedAudio{}, fmt.Errorf("timed out waiting for ACE-Step audio output: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func fetchACEStepTaskResult(ctx context.Context, client *http.Client, endpoint *url.URL, taskID string) (aceStepGeneratedAudio, bool, error) {
	reqBody, err := json.Marshal(map[string]interface{}{"task_id_list": []string{taskID}})
	if err != nil {
		return aceStepGeneratedAudio{}, false, fmt.Errorf("failed to encode ACE-Step query request: %w", err)
	}

	queryURL := *endpoint
	queryURL.Path = "/query_result"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, queryURL.String(), bytes.NewReader(reqBody))
	if err != nil {
		return aceStepGeneratedAudio{}, false, fmt.Errorf("failed to create ACE-Step query request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return aceStepGeneratedAudio{}, false, fmt.Errorf("failed to query ACE-Step task: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return aceStepGeneratedAudio{}, false, fmt.Errorf("ACE-Step query returned status %d: %s", resp.StatusCode, readLimitedErrorBody(resp.Body))
	}

	var envelope aceStepEnvelope
	if err := decodeLimitedJSON(resp.Body, maxACEStepJSONResponseBytes, "ACE-Step query response", &envelope); err != nil {
		return aceStepGeneratedAudio{}, false, fmt.Errorf("failed to decode ACE-Step query response: %w", err)
	}
	if err := validateACEStepEnvelope(envelope, "query"); err != nil {
		return aceStepGeneratedAudio{}, false, err
	}
	var items []aceStepQueryItem
	if err := json.Unmarshal(envelope.Data, &items); err != nil {
		return aceStepGeneratedAudio{}, false, fmt.Errorf("failed to decode ACE-Step query data: %w", err)
	}
	for _, item := range items {
		if item.TaskID != taskID {
			continue
		}
		switch item.Status {
		case 0:
			return aceStepGeneratedAudio{}, false, nil
		case 1:
			generated, err := parseACEStepResultPayload(item.Result)
			if err != nil {
				return aceStepGeneratedAudio{}, false, err
			}
			return generated, true, nil
		case 2:
			msg := strings.TrimSpace(item.ProgressText)
			if msg == "" {
				msg = strings.TrimSpace(item.Result)
			}
			if msg == "" {
				msg = "generation failed"
			}
			return aceStepGeneratedAudio{}, false, fmt.Errorf("ACE-Step task failed: %s", msg)
		default:
			return aceStepGeneratedAudio{}, false, fmt.Errorf("ACE-Step query returned unknown status %d", item.Status)
		}
	}
	return aceStepGeneratedAudio{}, false, nil
}

func validateACEStepEnvelope(envelope aceStepEnvelope, label string) error {
	if envelope.Code != 0 && envelope.Code != http.StatusOK {
		msg := ""
		if envelope.Error != nil {
			msg = strings.TrimSpace(*envelope.Error)
		}
		if msg == "" {
			msg = "unknown error"
		}
		return fmt.Errorf("ACE-Step %s response returned code %d: %s", label, envelope.Code, msg)
	}
	return nil
}

func parseACEStepResultPayload(raw string) (aceStepGeneratedAudio, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return aceStepGeneratedAudio{}, fmt.Errorf("ACE-Step result payload is empty")
	}
	var files []aceStepGeneratedAudio
	if err := json.Unmarshal([]byte(raw), &files); err != nil {
		return aceStepGeneratedAudio{}, fmt.Errorf("failed to decode ACE-Step result payload: %w", err)
	}
	for _, item := range files {
		if strings.TrimSpace(item.File) != "" {
			return item, nil
		}
	}
	return aceStepGeneratedAudio{}, fmt.Errorf("ACE-Step result did not include an audio file")
}

func downloadACEStepAudio(ctx context.Context, client *http.Client, endpoint *url.URL, remoteFile string, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = defaultAudioMaxAudioBytes
	}
	audioURL, err := resolveACEStepAudioURL(endpoint, remoteFile)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, audioURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create ACE-Step audio download request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download ACE-Step audio: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ACE-Step audio download returned status %d: %s", resp.StatusCode, readLimitedErrorBody(resp.Body))
	}
	if resp.ContentLength > maxBytes {
		return nil, fmt.Errorf("ACE-Step audio exceeds max_audio_bytes (%d)", maxBytes)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read ACE-Step audio response: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("ACE-Step audio exceeds max_audio_bytes (%d)", maxBytes)
	}
	canonicalData, err := canonicalizeWAVAudio(data, resp.Header.Get("Content-Type"))
	if err != nil {
		return nil, err
	}
	return canonicalData, nil
}

func resolveACEStepAudioURL(endpoint *url.URL, remoteFile string) (*url.URL, error) {
	remoteFile = strings.TrimSpace(remoteFile)
	if remoteFile == "" {
		return nil, fmt.Errorf("ACE-Step audio result did not include a file URL")
	}
	parsed, err := url.Parse(remoteFile)
	if err != nil {
		return nil, fmt.Errorf("ACE-Step audio file URL is invalid: %w", err)
	}
	var audioURL url.URL
	if parsed.IsAbs() {
		if parsed.Scheme != "http" || parsed.User != nil || parsed.Fragment != "" {
			return nil, fmt.Errorf("ACE-Step audio file URL is not an allowed local HTTP URL")
		}
		if !strings.EqualFold(parsed.Host, endpoint.Host) {
			return nil, fmt.Errorf("ACE-Step audio file URL must use the configured endpoint host")
		}
		audioURL = *parsed
	} else {
		if parsed.Host != "" || parsed.Scheme != "" || parsed.Fragment != "" {
			return nil, fmt.Errorf("ACE-Step audio file URL is not an allowed relative path")
		}
		audioURL = *endpoint
		audioURL.Path = parsed.Path
		audioURL.RawQuery = parsed.RawQuery
	}
	if audioURL.Path != "/v1/audio" {
		return nil, fmt.Errorf("ACE-Step audio file URL must use /v1/audio")
	}
	if _, err := validateACEStepEndpoint((&url.URL{Scheme: audioURL.Scheme, Host: audioURL.Host}).String()); err != nil {
		return nil, err
	}
	return &audioURL, nil
}

func ensureAudioArtifactOutputDir(outputDir string, root string) (string, error) {
	dir, targetAbs, err := openAudioArtifactOutputDir(outputDir, root)
	if err != nil {
		return "", err
	}
	if err := dir.Close(); err != nil {
		return "", fmt.Errorf("audio generation config error: failed to close output_dir descriptor: %w", err)
	}
	return targetAbs, nil
}

func openAudioArtifactOutputDir(outputDir string, root string) (*os.File, string, error) {
	outputDir = strings.TrimSpace(outputDir)
	if outputDir == "" {
		return nil, "", fmt.Errorf("audio generation config error: output_dir is required")
	}

	fixedBase := filepath.Clean(filepath.Join(root, defaultAudioOutputDir))
	if err := ensureContained(fixedBase, root); err != nil {
		return nil, "", fmt.Errorf("audio generation config error: fixed audio artifact base rejected: %w", err)
	}

	target := outputDir
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return nil, "", fmt.Errorf("audio generation config error: output_dir rejected: %w", err)
	}
	targetAbs = filepath.Clean(targetAbs)
	if err := ensureContained(targetAbs, root); err != nil {
		return nil, "", fmt.Errorf("audio generation config error: output_dir rejected: %w", err)
	}
	if err := ensureContained(targetAbs, fixedBase); err != nil {
		return nil, "", fmt.Errorf("audio generation config error: output_dir must stay under %s: %w", defaultAudioOutputDir, err)
	}

	rel, err := filepath.Rel(root, targetAbs)
	if err != nil {
		return nil, "", fmt.Errorf("audio generation config error: output_dir rejected: %w", err)
	}

	currentFD, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, "", fmt.Errorf("audio generation config error: failed to open workspace root descriptor: %w", err)
	}
	closeCurrent := true
	defer func() {
		if closeCurrent {
			_ = unix.Close(currentFD)
		}
	}()

	for _, component := range strings.Split(rel, string(os.PathSeparator)) {
		if component == "" || component == "." {
			continue
		}
		nextFD, err := openDirAtNoFollow(currentFD, component)
		if err != nil {
			if err != unix.ENOENT {
				return nil, "", formatAudioOpenDirAtError("output_dir", component, err)
			}
			if err := unix.Mkdirat(currentFD, component, 0755); err != nil && err != unix.EEXIST {
				return nil, "", fmt.Errorf("audio generation config error: failed to create output_dir component %q: %w", component, err)
			}
			nextFD, err = openDirAtNoFollow(currentFD, component)
			if err != nil {
				return nil, "", formatAudioOpenDirAtError("output_dir", component, err)
			}
		}
		if err := unix.Close(currentFD); err != nil {
			_ = unix.Close(nextFD)
			return nil, "", fmt.Errorf("audio generation config error: failed to close output_dir parent descriptor: %w", err)
		}
		currentFD = nextFD
	}

	closeCurrent = false
	return os.NewFile(uintptr(currentFD), targetAbs), targetAbs, nil
}

func formatAudioOpenDirAtError(label string, component string, err error) error {
	switch err {
	case unix.ELOOP:
		return fmt.Errorf("audio generation config error: %s symlink component is not allowed: %s", label, component)
	case unix.ENOTDIR:
		return fmt.Errorf("audio generation config error: %s component is not a directory or is a symlink: %s", label, component)
	default:
		return fmt.Errorf("audio generation config error: failed to open %s component %q: %w", label, component, err)
	}
}

func writeAudioArtifact(ctx context.Context, outputDirSetting string, root string, taskID string, remoteFile string, data []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	remoteSegment := sanitizeArtifactSegment(remoteAudioFilename(remoteFile), "audio", maxArtifactNameSegmentRunes)
	remoteBase := strings.TrimSuffix(remoteSegment, filepath.Ext(remoteSegment))
	remoteBase = strings.Trim(remoteBase, "._-")
	if remoteBase == "" {
		remoteBase = "audio"
	}
	remoteSegment = remoteBase + ".wav"
	taskSegment := sanitizeArtifactSegment(taskID, "task", 48)
	timestamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	localName := fmt.Sprintf("%s_%s_%s", timestamp, taskSegment, remoteSegment)

	outputDirFile, outputDir, err := openAudioArtifactOutputDir(outputDirSetting, root)
	if err != nil {
		return "", err
	}
	defer outputDirFile.Close()

	artifactPath := filepath.Join(outputDir, localName)
	artifactPath = filepath.Clean(artifactPath)
	if err := ensureContained(artifactPath, root); err != nil {
		return "", fmt.Errorf("audio artifact path rejected: %w", err)
	}
	if strings.ContainsAny(localName, `/\`) {
		return "", fmt.Errorf("audio artifact filename rejected")
	}
	outputDirFD := int(outputDirFile.Fd())
	artifactFD, err := unix.Openat(outputDirFD, localName, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return "", fmt.Errorf("failed to create audio artifact: %w", err)
	}
	file := os.NewFile(uintptr(artifactFD), artifactPath)
	writeErr := func() (err error) {
		defer func() {
			if closeErr := file.Close(); err == nil && closeErr != nil {
				err = fmt.Errorf("failed to close audio artifact: %w", closeErr)
			}
			if err != nil {
				_ = unix.Unlinkat(outputDirFD, localName, 0)
			}
		}()
		n, err := file.Write(data)
		if err != nil {
			return fmt.Errorf("failed to write audio artifact: %w", err)
		}
		if n != len(data) {
			return fmt.Errorf("failed to write complete audio artifact")
		}
		info, err := file.Stat()
		if err != nil {
			return fmt.Errorf("failed to inspect audio artifact descriptor: %w", err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("audio artifact descriptor is not a regular file")
		}
		return file.Sync()
	}()
	if writeErr != nil {
		return "", writeErr
	}
	if err := unix.Fsync(outputDirFD); err != nil {
		return "", fmt.Errorf("failed to sync audio artifact directory: %w", err)
	}
	return artifactPath, nil
}

func remoteAudioFilename(remoteFile string) string {
	parsed, err := url.Parse(strings.TrimSpace(remoteFile))
	if err == nil {
		if audioPath := parsed.Query().Get("path"); audioPath != "" {
			if base := urlpath.Base(audioPath); base != "." && base != "/" {
				return base
			}
		}
		if parsed.Path != "" {
			if base := urlpath.Base(parsed.Path); base != "." && base != "/" {
				return base
			}
		}
	}
	return remoteFile
}

func canonicalizeWAVAudio(data []byte, contentType string) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("ACE-Step audio response is empty")
	}
	if contentType != "" {
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err == nil && mediaType != "" && mediaType != "application/octet-stream" && !isAllowedWAVMediaType(mediaType) {
			return nil, fmt.Errorf("ACE-Step audio content type %q is not an allowed WAV type", mediaType)
		}
	}
	if len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, fmt.Errorf("ACE-Step audio content is not a valid RIFF/WAVE file")
	}

	declaredSize := int64(binary.LittleEndian.Uint32(data[4:8]))
	declaredEnd := declaredSize + 8
	if declaredEnd < 12 || declaredEnd > int64(len(data)) {
		return nil, fmt.Errorf("ACE-Step audio RIFF size is invalid")
	}
	riffData := data[:declaredEnd]

	var fmtChunk []byte
	var audioData []byte
	for offset := 12; offset+8 <= len(riffData); {
		chunkID := string(riffData[offset : offset+4])
		chunkSize := int(binary.LittleEndian.Uint32(riffData[offset+4 : offset+8]))
		chunkStart := offset + 8
		chunkEnd := chunkStart + chunkSize
		if chunkSize < 0 || chunkEnd > len(riffData) {
			return nil, fmt.Errorf("ACE-Step audio chunk %q exceeds RIFF bounds", chunkID)
		}
		chunk := riffData[chunkStart:chunkEnd]
		switch chunkID {
		case "fmt ":
			if fmtChunk == nil {
				fmtChunk = append([]byte(nil), chunk...)
			}
		case "data":
			if audioData == nil {
				audioData = append([]byte(nil), chunk...)
			}
		}
		next := chunkEnd
		if chunkSize%2 == 1 {
			next++
		}
		if next > len(riffData) {
			return nil, fmt.Errorf("ACE-Step audio chunk %q padding exceeds RIFF bounds", chunkID)
		}
		offset = next
	}
	if fmtChunk == nil {
		return nil, fmt.Errorf("ACE-Step audio WAV fmt chunk is missing")
	}
	if audioData == nil || len(audioData) == 0 {
		return nil, fmt.Errorf("ACE-Step audio WAV data chunk is missing or empty")
	}
	if err := validateWAVFmtChunk(fmtChunk, len(audioData)); err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	buf.WriteString("RIFF")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(0))
	buf.WriteString("WAVE")
	writeWAVChunk(&buf, "fmt ", fmtChunk)
	writeWAVChunk(&buf, "data", audioData)
	out := buf.Bytes()
	binary.LittleEndian.PutUint32(out[4:8], uint32(len(out)-8))
	return out, nil
}

func isAllowedWAVMediaType(mediaType string) bool {
	switch mediaType {
	case "audio/wav", "audio/wave", "audio/x-wav", "audio/vnd.wave":
		return true
	default:
		return false
	}
}

func validateWAVFmtChunk(fmtChunk []byte, dataSize int) error {
	if len(fmtChunk) < 16 {
		return fmt.Errorf("ACE-Step audio WAV fmt chunk is too short")
	}
	audioFormat := binary.LittleEndian.Uint16(fmtChunk[0:2])
	channels := binary.LittleEndian.Uint16(fmtChunk[2:4])
	sampleRate := binary.LittleEndian.Uint32(fmtChunk[4:8])
	byteRate := binary.LittleEndian.Uint32(fmtChunk[8:12])
	blockAlign := binary.LittleEndian.Uint16(fmtChunk[12:14])
	bitsPerSample := binary.LittleEndian.Uint16(fmtChunk[14:16])

	if audioFormat != 1 && audioFormat != 3 {
		return fmt.Errorf("ACE-Step audio WAV format %d is not supported", audioFormat)
	}
	if channels == 0 || channels > 64 {
		return fmt.Errorf("ACE-Step audio WAV channel count is invalid")
	}
	if sampleRate == 0 || sampleRate > 384000 {
		return fmt.Errorf("ACE-Step audio WAV sample rate is invalid")
	}
	if byteRate == 0 || blockAlign == 0 {
		return fmt.Errorf("ACE-Step audio WAV byte rate or block align is invalid")
	}
	switch bitsPerSample {
	case 8, 16, 24, 32, 64:
	default:
		return fmt.Errorf("ACE-Step audio WAV bits per sample is invalid")
	}
	if dataSize%int(blockAlign) != 0 {
		return fmt.Errorf("ACE-Step audio WAV data size does not align to block size")
	}
	return nil
}

func writeWAVChunk(buf *bytes.Buffer, chunkID string, data []byte) {
	buf.WriteString(chunkID)
	_ = binary.Write(buf, binary.LittleEndian, uint32(len(data)))
	buf.Write(data)
	if len(data)%2 == 1 {
		buf.WriteByte(0)
	}
}
