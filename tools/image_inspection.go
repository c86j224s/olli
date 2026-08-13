package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/c86j224s/olli/ollama"
	"golang.org/x/sys/unix"
)

const (
	imageInspectToolName            = "inspect_image"
	defaultImageInspectionEndpoint  = "http://127.0.0.1:11434"
	defaultImageInspectionModel     = "gemma4:12b"
	defaultImageInspectionTimeout   = 300
	defaultImageInspectionMaxBytes  = 67108864
	maxImageInspectionTimeout       = 900
	maxImageInspectionQuestionRunes = 8192
	maxOllamaJSONResponseBytes      = 8388608
)

const defaultImageInspectionQuestion = "Describe the image, assess its visible quality and likely prompt compliance, identify visible defects, and report uncertainty."

type ImageInspectionConfig struct {
	Ollama OllamaImageInspectionConfig
}

type OllamaImageInspectionConfig struct {
	Endpoint       string
	Model          string
	TimeoutSeconds int
	MaxImageBytes  int64
}

type imageInspectionAssessment struct {
	Description   string   `json:"description"`
	Answer        string   `json:"answer"`
	QualityIssues []string `json:"quality_issues"`
	Uncertainties []string `json:"uncertainties"`
	Confidence    float64  `json:"confidence"`
}

type imageInspectionResult struct {
	Model      string                    `json:"model"`
	Path       string                    `json:"path"`
	Width      int                       `json:"width"`
	Height     int                       `json:"height"`
	MediaType  string                    `json:"media_type"`
	Question   string                    `json:"question"`
	Assessment imageInspectionAssessment `json:"assessment"`
	Disclaimer string                    `json:"disclaimer"`
}

func DefaultImageInspectionConfig() ImageInspectionConfig {
	return ImageInspectionConfig{
		Ollama: OllamaImageInspectionConfig{
			Endpoint:       defaultImageInspectionEndpoint,
			Model:          defaultImageInspectionModel,
			TimeoutSeconds: defaultImageInspectionTimeout,
			MaxImageBytes:  defaultImageInspectionMaxBytes,
		},
	}
}

func (c ImageInspectionConfig) withDefaults() ImageInspectionConfig {
	defaults := DefaultImageInspectionConfig()
	if strings.TrimSpace(c.Ollama.Endpoint) == "" {
		c.Ollama.Endpoint = defaults.Ollama.Endpoint
	}
	if strings.TrimSpace(c.Ollama.Model) == "" {
		c.Ollama.Model = defaults.Ollama.Model
	}
	if c.Ollama.TimeoutSeconds <= 0 {
		c.Ollama.TimeoutSeconds = defaults.Ollama.TimeoutSeconds
	}
	if c.Ollama.MaxImageBytes <= 0 {
		c.Ollama.MaxImageBytes = defaults.Ollama.MaxImageBytes
	}
	return c
}

func (r *Registry) SetImageInspectionConfig(cfg ImageInspectionConfig) {
	r.imageInspection = cfg.withDefaults()
}

func validateImageInspectionRuntimeConfig(cfg ImageInspectionConfig) error {
	if strings.TrimSpace(cfg.Ollama.Model) == "" {
		return fmt.Errorf("image inspection config error: model is required")
	}
	if cfg.Ollama.TimeoutSeconds <= 0 || cfg.Ollama.TimeoutSeconds > maxImageInspectionTimeout {
		return fmt.Errorf("image inspection config error: timeout_seconds must be between 1 and %d", maxImageInspectionTimeout)
	}
	if cfg.Ollama.MaxImageBytes <= 0 || cfg.Ollama.MaxImageBytes > maxImageMaxImageBytes {
		return fmt.Errorf("image inspection config error: max_image_bytes must be between 1 and %d", maxImageMaxImageBytes)
	}
	return nil
}

func (r *Registry) registerImageInspectTool() {
	r.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        imageInspectToolName,
			Description: "Visually inspect a generated image under artifacts/images with the configured local Ollama vision model. Use this after image_generate when visual quality or prompt compliance must be verified.",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"path": {
						Type:        "string",
						Description: "Workspace-relative path to a generated image under artifacts/images",
					},
					"question": {
						Type:        "string",
						Description: "Optional visual question or verification criteria, such as whether the image contains exactly four visible legs",
					},
				},
				Required: []string{"path"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		return r.executeImageInspect(args)
	})
}

func (r *Registry) executeImageInspect(args map[string]interface{}) (string, error) {
	cfg := r.imageInspection.withDefaults()
	if err := validateImageInspectionRuntimeConfig(cfg); err != nil {
		return "", err
	}

	pathArg, err := requiredStringArg(args, "path")
	if err != nil {
		return "", err
	}
	question, hasQuestion, err := optionalStringArg(args, "question")
	if err != nil {
		return "", err
	}
	question = strings.TrimSpace(question)
	if !hasQuestion || question == "" {
		question = defaultImageInspectionQuestion
	}
	if len([]rune(question)) > maxImageInspectionQuestionRunes {
		return "", fmt.Errorf("invalid question argument: must be at most %d characters", maxImageInspectionQuestionRunes)
	}

	endpoint, err := validateImageInspectionEndpoint(cfg.Ollama.Endpoint)
	if err != nil {
		return "", err
	}
	root, err := IsPathSafeFrom(".", r.GetWorkspaceRoot(), r.GetWorkspaceRoot())
	if err != nil {
		return "", fmt.Errorf("image inspection config error: workspace root rejected: %w", err)
	}
	imageBytes, relativePath, width, height, err := readImageInspectionArtifact(pathArg, root, cfg.Ollama.MaxImageBytes)
	if err != nil {
		return "", err
	}

	timeout := time.Duration(cfg.Ollama.TimeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	client := newLocalImageInspectionHTTPClient(timeout)
	if err := requireOllamaVisionModel(ctx, client, endpoint, cfg.Ollama.Model); err != nil {
		return "", err
	}
	assessment, err := requestImageAssessment(ctx, client, endpoint, cfg.Ollama.Model, question, imageBytes)
	if err != nil {
		return "", err
	}

	result := imageInspectionResult{
		Model:      cfg.Ollama.Model,
		Path:       relativePath,
		Width:      width,
		Height:     height,
		MediaType:  "image/png",
		Question:   question,
		Assessment: assessment,
		Disclaimer: "Vision-model assessment; not ground truth. Verify consequential details independently.",
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal image inspection result: %w", err)
	}
	return string(data), nil
}

func readImageInspectionArtifact(pathArg string, root string, maxBytes int64) ([]byte, string, int, int, error) {
	pathArg = strings.TrimSpace(pathArg)
	if pathArg == "" || filepath.IsAbs(pathArg) {
		return nil, "", 0, 0, fmt.Errorf("image inspection security block: path must be workspace-relative under %s", defaultImageOutputDir)
	}

	artifactRoot := filepath.Clean(filepath.Join(root, defaultImageOutputDir))
	target := filepath.Clean(filepath.Join(root, pathArg))
	if err := ensureContained(target, artifactRoot); err != nil {
		return nil, "", 0, 0, fmt.Errorf("image inspection security block: path must stay under %s: %w", defaultImageOutputDir, err)
	}
	relativeToArtifacts, err := filepath.Rel(artifactRoot, target)
	if err != nil || relativeToArtifacts == "." {
		return nil, "", 0, 0, fmt.Errorf("image inspection security block: path must name an image file under %s", defaultImageOutputDir)
	}

	rootFD, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, "", 0, 0, fmt.Errorf("image inspection failed to open workspace root: %w", err)
	}
	currentFD := rootFD
	defer func() {
		_ = unix.Close(currentFD)
	}()

	for _, component := range strings.Split(defaultImageOutputDir, string(os.PathSeparator)) {
		nextFD, openErr := openDirAtNoFollow(currentFD, component)
		if openErr != nil {
			return nil, "", 0, 0, fmt.Errorf("image inspection failed to open artifact directory %q: %w", component, openErr)
		}
		_ = unix.Close(currentFD)
		currentFD = nextFD
	}

	components := strings.Split(relativeToArtifacts, string(os.PathSeparator))
	for _, component := range components[:len(components)-1] {
		if component == "" || component == "." || component == ".." {
			return nil, "", 0, 0, fmt.Errorf("image inspection security block: invalid path component")
		}
		nextFD, openErr := openDirAtNoFollow(currentFD, component)
		if openErr != nil {
			return nil, "", 0, 0, fmt.Errorf("image inspection failed to open path component %q: %w", component, openErr)
		}
		_ = unix.Close(currentFD)
		currentFD = nextFD
	}

	name := components[len(components)-1]
	if name == "" || name == "." || name == ".." {
		return nil, "", 0, 0, fmt.Errorf("image inspection security block: invalid image filename")
	}
	fd, err := unix.Openat(currentFD, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, "", 0, 0, fmt.Errorf("image inspection failed to open image: %w", err)
	}
	file := os.NewFile(uintptr(fd), target)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, "", 0, 0, fmt.Errorf("image inspection failed to inspect image: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, "", 0, 0, fmt.Errorf("image inspection path must be a regular file")
	}
	if info.Size() > maxBytes {
		return nil, "", 0, 0, fmt.Errorf("image inspection input exceeds max_image_bytes (%d)", maxBytes)
	}
	data, err := readAllLimited(file, maxBytes, "image inspection input")
	if err != nil {
		return nil, "", 0, 0, err
	}
	canonical, _, err := canonicalizeRasterImage(data, http.DetectContentType(data))
	if err != nil {
		return nil, "", 0, 0, fmt.Errorf("image inspection rejected image: %w", err)
	}
	decoded, _, err := image.DecodeConfig(bytes.NewReader(canonical))
	if err != nil {
		return nil, "", 0, 0, fmt.Errorf("image inspection failed to read canonical dimensions: %w", err)
	}
	return canonical, filepath.Clean(pathArg), decoded.Width, decoded.Height, nil
}

func validateImageInspectionEndpoint(rawEndpoint string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawEndpoint))
	if err != nil {
		return nil, fmt.Errorf("image inspection config error: invalid Ollama endpoint: %w", err)
	}
	if parsed.Scheme != "http" {
		return nil, fmt.Errorf("image inspection config error: Ollama endpoint scheme must be http")
	}
	if parsed.User != nil {
		return nil, fmt.Errorf("image inspection config error: Ollama endpoint must not include userinfo")
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("image inspection config error: Ollama endpoint host is required")
	}
	if parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("image inspection config error: Ollama endpoint must not include path, query, or fragment")
	}
	switch parsed.Hostname() {
	case "127.0.0.1", "::1", "localhost":
	default:
		return nil, fmt.Errorf("image inspection config error: Ollama endpoint host must be loopback/local")
	}
	return parsed, nil
}

func newLocalImageInspectionHTTPClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
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
				return nil, fmt.Errorf("image inspection security block: Ollama connection resolved outside loopback")
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

func requireOllamaVisionModel(ctx context.Context, client *http.Client, endpoint *url.URL, model string) error {
	body, err := json.Marshal(map[string]string{"model": model})
	if err != nil {
		return fmt.Errorf("failed to encode Ollama model request: %w", err)
	}
	showURL := *endpoint
	showURL.Path = "/api/show"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, showURL.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create Ollama model request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to inspect Ollama vision model %q: %w", model, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Ollama model request returned status %d: %s", resp.StatusCode, readLimitedErrorBody(resp.Body))
	}
	var result struct {
		Capabilities []string `json:"capabilities"`
	}
	if err := decodeLimitedJSON(resp.Body, maxOllamaJSONResponseBytes, "Ollama model response", &result); err != nil {
		return fmt.Errorf("failed to decode Ollama model response: %w", err)
	}
	for _, capability := range result.Capabilities {
		if capability == "vision" {
			return nil
		}
	}
	return fmt.Errorf("image inspection config error: Ollama model %q does not advertise vision capability", model)
}

func requestImageAssessment(ctx context.Context, client *http.Client, endpoint *url.URL, model string, question string, imageBytes []byte) (imageInspectionAssessment, error) {
	reqBody := ollama.ChatRequest{
		Model: model,
		Messages: []ollama.Message{
			{
				Role:    "system",
				Content: "You are a careful visual inspector. Answer only from visible evidence, follow the JSON schema, and put uncertain or ambiguous claims in uncertainties. Confidence must be between 0 and 1.",
			},
			{
				Role:    "user",
				Content: question,
				Images:  []string{base64.StdEncoding.EncodeToString(imageBytes)},
			},
		},
		Format: imageInspectionJSONSchema(),
		Options: &ollama.Options{
			Temperature: 0.1,
		},
		Stream: false,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return imageInspectionAssessment{}, fmt.Errorf("failed to encode Ollama image inspection request: %w", err)
	}
	chatURL := *endpoint
	chatURL.Path = "/api/chat"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, chatURL.String(), bytes.NewReader(body))
	if err != nil {
		return imageInspectionAssessment{}, fmt.Errorf("failed to create Ollama image inspection request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return imageInspectionAssessment{}, fmt.Errorf("Ollama image inspection request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return imageInspectionAssessment{}, fmt.Errorf("Ollama image inspection returned status %d: %s", resp.StatusCode, readLimitedErrorBody(resp.Body))
	}
	var response ollama.ChatResponseChunk
	if err := decodeLimitedJSON(resp.Body, maxOllamaJSONResponseBytes, "Ollama image inspection response", &response); err != nil {
		return imageInspectionAssessment{}, fmt.Errorf("failed to decode Ollama image inspection response: %w", err)
	}
	assessment, err := decodeImageInspectionAssessment(response.Message.Content)
	if err != nil {
		return imageInspectionAssessment{}, err
	}
	return assessment, nil
}

func decodeImageInspectionAssessment(content string) (imageInspectionAssessment, error) {
	type imageInspectionAssessmentWire struct {
		Description   *string   `json:"description"`
		Answer        *string   `json:"answer"`
		QualityIssues *[]string `json:"quality_issues"`
		Uncertainties *[]string `json:"uncertainties"`
		Confidence    *float64  `json:"confidence"`
	}

	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.DisallowUnknownFields()
	var wire imageInspectionAssessmentWire
	if err := decoder.Decode(&wire); err != nil {
		return imageInspectionAssessment{}, fmt.Errorf("Ollama image inspection response was not valid structured JSON: %w", err)
	}
	var extra interface{}
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return imageInspectionAssessment{}, fmt.Errorf("Ollama image inspection response was not valid structured JSON: trailing JSON value")
		}
		return imageInspectionAssessment{}, fmt.Errorf("Ollama image inspection response was not valid structured JSON: trailing content: %w", err)
	}
	if wire.Description == nil || wire.Answer == nil || wire.QualityIssues == nil || wire.Uncertainties == nil || wire.Confidence == nil {
		return imageInspectionAssessment{}, fmt.Errorf("Ollama image inspection response omitted required assessment fields")
	}
	assessment := imageInspectionAssessment{
		Description:   *wire.Description,
		Answer:        *wire.Answer,
		QualityIssues: *wire.QualityIssues,
		Uncertainties: *wire.Uncertainties,
		Confidence:    *wire.Confidence,
	}
	if strings.TrimSpace(assessment.Description) == "" || strings.TrimSpace(assessment.Answer) == "" {
		return imageInspectionAssessment{}, fmt.Errorf("Ollama image inspection response omitted required assessment fields")
	}
	if assessment.Confidence < 0 || assessment.Confidence > 1 {
		return imageInspectionAssessment{}, fmt.Errorf("Ollama image inspection confidence must be between 0 and 1")
	}
	return assessment, nil
}

func imageInspectionJSONSchema() map[string]interface{} {
	return map[string]interface{}{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"description", "answer", "quality_issues", "uncertainties", "confidence"},
		"properties": map[string]interface{}{
			"description": map[string]interface{}{"type": "string"},
			"answer":      map[string]interface{}{"type": "string"},
			"quality_issues": map[string]interface{}{
				"type":  "array",
				"items": map[string]interface{}{"type": "string"},
			},
			"uncertainties": map[string]interface{}{
				"type":  "array",
				"items": map[string]interface{}{"type": "string"},
			},
			"confidence": map[string]interface{}{
				"type":    "number",
				"minimum": 0,
				"maximum": 1,
			},
		},
	}
}
