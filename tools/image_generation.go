package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"math"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/c86j224s/olli/ollama"
	"golang.org/x/sys/unix"
)

const (
	imageGenerateToolName       = "image_generate"
	comfyUIBackendAlias         = "comfyui"
	defaultComfyUIEndpoint      = "http://127.0.0.1:8188"
	defaultImageOutputDir       = "artifacts/images"
	defaultImageTimeoutSeconds  = 300
	defaultImageMaxImageBytes   = 67108864
	maxImageTimeoutSeconds      = 900
	maxImageMaxImageBytes       = 134217728
	maxWorkflowJSONBytes        = 16777216
	maxComfyJSONResponseBytes   = 8388608
	maxImageDimension           = 4096
	maxImageSteps               = 200
	imageHTTPPollInterval       = 250 * time.Millisecond
	maxArtifactNameSegmentRunes = 96
)

type ImageGenerationConfig struct {
	ComfyUI ComfyUIConfig
}

type ComfyUIConfig struct {
	Endpoint       string
	OutputDir      string
	TimeoutSeconds int
	MaxImageBytes  int64
	Workflows      map[string]ComfyUIWorkflowConfig
}

type ComfyUIWorkflowConfig struct {
	Path                 string
	PromptNodeID         string
	PromptInput          string
	NegativePromptNodeID string
	NegativePromptInput  string
	WidthNodeID          string
	WidthInput           string
	HeightNodeID         string
	HeightInput          string
	StepsNodeID          string
	StepsInput           string
	SeedNodeID           string
	SeedInput            string
}

type comfyImageRef struct {
	Filename  string `json:"filename"`
	Subfolder string `json:"subfolder"`
	Type      string `json:"type"`
}

type comfyHistoryEntry struct {
	Outputs map[string]comfyHistoryOutput `json:"outputs"`
}

type comfyHistoryOutput struct {
	Images []comfyImageRef `json:"images"`
}

type imageGenerateResult struct {
	BackendAlias          string `json:"backend_alias"`
	WorkflowAlias         string `json:"workflow_alias"`
	PromptID              string `json:"prompt_id"`
	ArtifactPath          string `json:"artifact_path"`
	WorkspaceRelativePath string `json:"workspace_relative_path"`
	Bytes                 int    `json:"bytes"`
	RemoteFilename        string `json:"remote_filename"`
	RemoteSubfolder       string `json:"remote_subfolder"`
	RemoteType            string `json:"remote_type"`
}

func DefaultImageGenerationConfig() ImageGenerationConfig {
	return ImageGenerationConfig{
		ComfyUI: ComfyUIConfig{
			Endpoint:       defaultComfyUIEndpoint,
			OutputDir:      defaultImageOutputDir,
			TimeoutSeconds: defaultImageTimeoutSeconds,
			MaxImageBytes:  defaultImageMaxImageBytes,
			Workflows:      map[string]ComfyUIWorkflowConfig{},
		},
	}
}

func (c ImageGenerationConfig) withDefaults() ImageGenerationConfig {
	defaults := DefaultImageGenerationConfig()
	if c.ComfyUI.Endpoint == "" {
		c.ComfyUI.Endpoint = defaults.ComfyUI.Endpoint
	}
	if c.ComfyUI.OutputDir == "" {
		c.ComfyUI.OutputDir = defaults.ComfyUI.OutputDir
	}
	if c.ComfyUI.TimeoutSeconds <= 0 {
		c.ComfyUI.TimeoutSeconds = defaults.ComfyUI.TimeoutSeconds
	}
	if c.ComfyUI.MaxImageBytes <= 0 {
		c.ComfyUI.MaxImageBytes = defaults.ComfyUI.MaxImageBytes
	}
	if c.ComfyUI.Workflows == nil {
		c.ComfyUI.Workflows = map[string]ComfyUIWorkflowConfig{}
	}
	return c
}

func (r *Registry) SetImageGenerationConfig(cfg ImageGenerationConfig) {
	cfg = cfg.withDefaults()
	workflows := make(map[string]ComfyUIWorkflowConfig, len(cfg.ComfyUI.Workflows))
	for alias, workflow := range cfg.ComfyUI.Workflows {
		workflows[alias] = workflow
	}
	cfg.ComfyUI.Workflows = workflows
	r.imageGeneration = cfg
	r.updateImageGenerateToolDefinition()
}

func configuredWorkflowAliases(workflows map[string]ComfyUIWorkflowConfig) []string {
	aliases := make([]string, 0, len(workflows))
	for alias := range workflows {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	return aliases
}

func workflowAliasProperty(workflows map[string]ComfyUIWorkflowConfig) ollama.FunctionParamProperty {
	aliases := configuredWorkflowAliases(workflows)
	description := "Configured ComfyUI workflow alias from image_generation.comfyui.workflows."
	switch len(aliases) {
	case 0:
		description += " No workflow aliases are currently configured."
	case 1:
		description += fmt.Sprintf(" Use %q.", aliases[0])
	default:
		description += fmt.Sprintf(" Allowed values: %s.", strings.Join(aliases, ", "))
	}
	return ollama.FunctionParamProperty{
		Type:        "string",
		Description: description,
		Enum:        aliases,
	}
}

func (r *Registry) updateImageGenerateToolDefinition() {
	for i := range r.definitions {
		if r.definitions[i].Function.Name != imageGenerateToolName {
			continue
		}
		properties := r.definitions[i].Function.Parameters.Properties
		properties["workflow_alias"] = workflowAliasProperty(r.imageGeneration.ComfyUI.Workflows)
		return
	}
}

func validateImageGenerationRuntimeConfig(cfg ImageGenerationConfig) error {
	if cfg.ComfyUI.TimeoutSeconds <= 0 || cfg.ComfyUI.TimeoutSeconds > maxImageTimeoutSeconds {
		return fmt.Errorf("image generation config error: timeout_seconds must be between 1 and %d", maxImageTimeoutSeconds)
	}
	if cfg.ComfyUI.MaxImageBytes <= 0 || cfg.ComfyUI.MaxImageBytes > maxImageMaxImageBytes {
		return fmt.Errorf("image generation config error: max_image_bytes must be between 1 and %d", maxImageMaxImageBytes)
	}
	return nil
}

func (r *Registry) registerImageGenerateTool() {
	r.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        imageGenerateToolName,
			Description: "Generate an image through a configured local ComfyUI workflow and save the first output image as a workspace artifact. After success, call inspect_image when visual quality or prompt compliance must be verified.",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"backend_alias": {
						Type:        "string",
						Description: "Configured image backend alias. Only 'comfyui' is supported.",
						Enum:        []string{comfyUIBackendAlias},
					},
					"workflow_alias": {
						Type:        "string",
						Description: "Configured ComfyUI workflow alias from image_generation.comfyui.workflows",
					},
					"prompt": {
						Type:        "string",
						Description: "Positive image prompt",
					},
					"negative_prompt": {
						Type:        "string",
						Description: "Optional negative image prompt",
					},
					"width": {
						Type:        "integer",
						Description: "Optional output width to inject when the workflow maps a width node input",
					},
					"height": {
						Type:        "integer",
						Description: "Optional output height to inject when the workflow maps a height node input",
					},
					"steps": {
						Type:        "integer",
						Description: "Optional sampler step count to inject when the workflow maps a steps node input",
					},
					"seed": {
						Type:        "integer",
						Description: "Optional seed to inject when the workflow maps a seed node input",
					},
				},
				Required: []string{"backend_alias", "workflow_alias", "prompt"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		return r.executeImageGenerate(args)
	})
}

func (r *Registry) executeImageGenerate(args map[string]interface{}) (string, error) {
	cfg := r.imageGeneration.withDefaults()
	if err := validateImageGenerationRuntimeConfig(cfg); err != nil {
		return "", err
	}

	backendAlias, err := requiredStringArg(args, "backend_alias")
	if err != nil {
		return "", err
	}
	if backendAlias != comfyUIBackendAlias {
		return "", fmt.Errorf("image generation config error: backend_alias %q is not configured; allowed backend: %s", backendAlias, comfyUIBackendAlias)
	}

	if len(cfg.ComfyUI.Workflows) == 0 {
		return "", fmt.Errorf("image generation config error: no ComfyUI workflows configured; add image_generation.comfyui.workflows entries in config.json")
	}

	workflowAlias, err := imageWorkflowAliasArg(args, cfg.ComfyUI.Workflows)
	if err != nil {
		return "", err
	}
	workflowCfg, ok := cfg.ComfyUI.Workflows[workflowAlias]
	if !ok {
		return "", fmt.Errorf("image generation config error: workflow_alias %q is not configured", workflowAlias)
	}

	prompt, err := requiredStringArg(args, "prompt")
	if err != nil {
		return "", err
	}
	negativePrompt, hasNegativePrompt, err := optionalStringArg(args, "negative_prompt")
	if err != nil {
		return "", err
	}
	width, hasWidth, err := optionalBoundedPositiveIntArg(args, "width", maxImageDimension)
	if err != nil {
		return "", err
	}
	height, hasHeight, err := optionalBoundedPositiveIntArg(args, "height", maxImageDimension)
	if err != nil {
		return "", err
	}
	steps, hasSteps, err := optionalBoundedPositiveIntArg(args, "steps", maxImageSteps)
	if err != nil {
		return "", err
	}
	seed, hasSeed, err := optionalNonNegativeInt64Arg(args, "seed")
	if err != nil {
		return "", err
	}

	endpoint, err := validateComfyUIEndpoint(cfg.ComfyUI.Endpoint)
	if err != nil {
		return "", err
	}

	root, err := IsPathSafeFrom(".", r.GetWorkspaceRoot(), r.GetWorkspaceRoot())
	if err != nil {
		return "", fmt.Errorf("image generation config error: workspace root rejected: %w", err)
	}

	workflowPath, err := resolveWorkflowPath(workflowCfg.Path, root)
	if err != nil {
		return "", err
	}
	workflow, err := loadWorkflowJSON(workflowPath)
	if err != nil {
		return "", err
	}
	if err := mutateWorkflowInput(workflow, workflowCfg.PromptNodeID, workflowCfg.PromptInput, prompt, "prompt", true); err != nil {
		return "", err
	}
	if hasNegativePrompt {
		if err := mutateWorkflowInput(workflow, workflowCfg.NegativePromptNodeID, workflowCfg.NegativePromptInput, negativePrompt, "negative_prompt", true); err != nil {
			return "", err
		}
	}
	if hasWidth {
		if err := mutateWorkflowInput(workflow, workflowCfg.WidthNodeID, workflowCfg.WidthInput, width, "width", true); err != nil {
			return "", err
		}
	}
	if hasHeight {
		if err := mutateWorkflowInput(workflow, workflowCfg.HeightNodeID, workflowCfg.HeightInput, height, "height", true); err != nil {
			return "", err
		}
	}
	if hasSteps {
		if err := mutateWorkflowInput(workflow, workflowCfg.StepsNodeID, workflowCfg.StepsInput, steps, "steps", true); err != nil {
			return "", err
		}
	}
	if hasSeed {
		if err := mutateWorkflowInput(workflow, workflowCfg.SeedNodeID, workflowCfg.SeedInput, seed, "seed", true); err != nil {
			return "", err
		}
	}

	if _, err := ensureArtifactOutputDir(cfg.ComfyUI.OutputDir, root); err != nil {
		return "", err
	}

	timeout := time.Duration(cfg.ComfyUI.TimeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	client := newLocalComfyHTTPClient(timeout)
	promptID, err := submitComfyPrompt(ctx, client, endpoint, workflow)
	if err != nil {
		return "", err
	}
	imageRef, err := pollComfyHistory(ctx, client, endpoint, promptID)
	if err != nil {
		return "", err
	}
	imageBytes, imageExt, err := downloadComfyImage(ctx, client, endpoint, imageRef, cfg.ComfyUI.MaxImageBytes)
	if err != nil {
		return "", err
	}
	artifactPath, err := writeImageArtifact(cfg.ComfyUI.OutputDir, root, promptID, imageRef.Filename, imageExt, imageBytes)
	if err != nil {
		return "", err
	}

	relPath, relErr := filepath.Rel(root, artifactPath)
	if relErr != nil {
		relPath = artifactPath
	}
	result := imageGenerateResult{
		BackendAlias:          backendAlias,
		WorkflowAlias:         workflowAlias,
		PromptID:              promptID,
		ArtifactPath:          artifactPath,
		WorkspaceRelativePath: relPath,
		Bytes:                 len(imageBytes),
		RemoteFilename:        imageRef.Filename,
		RemoteSubfolder:       imageRef.Subfolder,
		RemoteType:            imageRef.Type,
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal image generation result: %w", err)
	}
	return string(data), nil
}

func imageWorkflowAliasArg(args map[string]interface{}, workflows map[string]ComfyUIWorkflowConfig) (string, error) {
	aliases := configuredWorkflowAliases(workflows)
	value, ok := args["workflow_alias"]
	if !ok || value == nil {
		if len(aliases) == 1 {
			return aliases[0], nil
		}
		return "", fmt.Errorf("invalid workflow_alias argument: choose one of %s", strings.Join(aliases, ", "))
	}
	alias, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("invalid workflow_alias argument")
	}
	alias = strings.TrimSpace(alias)
	if alias == "" {
		if len(aliases) == 1 {
			return aliases[0], nil
		}
		return "", fmt.Errorf("invalid workflow_alias argument: choose one of %s", strings.Join(aliases, ", "))
	}
	return alias, nil
}

func requiredStringArg(args map[string]interface{}, key string) (string, error) {
	value, ok := args[key]
	if !ok || value == nil {
		return "", fmt.Errorf("invalid %s argument", key)
	}
	str, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("invalid %s argument", key)
	}
	if strings.TrimSpace(str) == "" {
		return "", fmt.Errorf("invalid %s argument", key)
	}
	return str, nil
}

func optionalStringArg(args map[string]interface{}, key string) (string, bool, error) {
	value, ok := args[key]
	if !ok || value == nil {
		return "", false, nil
	}
	str, ok := value.(string)
	if !ok {
		return "", false, fmt.Errorf("invalid %s argument", key)
	}
	return str, true, nil
}

func optionalBoundedPositiveIntArg(args map[string]interface{}, key string, maxValue int) (int, bool, error) {
	value, ok := args[key]
	if !ok || value == nil {
		return 0, false, nil
	}
	n, err := int64FromToolValue(value, key)
	if err != nil {
		return 0, false, err
	}
	maxInt := int64(int(^uint(0) >> 1))
	if n <= 0 || n > maxInt {
		return 0, false, fmt.Errorf("invalid %s argument: must be a positive integer", key)
	}
	if n > int64(maxValue) {
		return 0, false, fmt.Errorf("invalid %s argument: must be <= %d", key, maxValue)
	}
	return int(n), true, nil
}

func optionalNonNegativeInt64Arg(args map[string]interface{}, key string) (int64, bool, error) {
	value, ok := args[key]
	if !ok || value == nil {
		return 0, false, nil
	}
	n, err := int64FromToolValue(value, key)
	if err != nil {
		return 0, false, err
	}
	if n < 0 {
		return 0, false, fmt.Errorf("invalid %s argument: must be a non-negative integer", key)
	}
	return n, true, nil
}

func int64FromToolValue(value interface{}, key string) (int64, error) {
	switch v := value.(type) {
	case int:
		return int64(v), nil
	case int8:
		return int64(v), nil
	case int16:
		return int64(v), nil
	case int32:
		return int64(v), nil
	case int64:
		return v, nil
	case float64:
		if math.Trunc(v) != v {
			return 0, fmt.Errorf("invalid %s argument: must be an integer", key)
		}
		n, err := strconv.ParseInt(strconv.FormatFloat(v, 'f', 0, 64), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid %s argument: must be an integer", key)
		}
		return n, nil
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			return 0, fmt.Errorf("invalid %s argument: must be an integer", key)
		}
		return n, nil
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid %s argument: must be an integer", key)
		}
		return n, nil
	default:
		return 0, fmt.Errorf("invalid %s argument: must be an integer", key)
	}
}

func validateComfyUIEndpoint(rawEndpoint string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawEndpoint))
	if err != nil {
		return nil, fmt.Errorf("image generation config error: invalid ComfyUI endpoint: %w", err)
	}
	if parsed.Scheme != "http" {
		return nil, fmt.Errorf("image generation config error: ComfyUI endpoint scheme must be http")
	}
	if parsed.User != nil {
		return nil, fmt.Errorf("image generation config error: ComfyUI endpoint must not include userinfo")
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("image generation config error: ComfyUI endpoint host is required")
	}
	if parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("image generation config error: ComfyUI endpoint must not include path, query, or fragment")
	}
	switch parsed.Hostname() {
	case "127.0.0.1", "::1", "localhost":
	default:
		return nil, fmt.Errorf("image generation config error: ComfyUI endpoint host must be loopback/local")
	}
	return parsed, nil
}

func newLocalComfyHTTPClient(timeout time.Duration) *http.Client {
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
				return nil, fmt.Errorf("image generation security block: ComfyUI connection resolved outside loopback")
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

func resolveWorkflowPath(workflowPath string, root string) (string, error) {
	workflowPath = strings.TrimSpace(workflowPath)
	if workflowPath == "" {
		return "", fmt.Errorf("image generation config error: workflow path is required")
	}

	safePath, err := IsPathSafeFrom(workflowPath, root, root)
	if err != nil {
		return "", fmt.Errorf("image generation config error: workflow path rejected: %w", err)
	}

	lexicalPath := workflowPath
	if !filepath.IsAbs(lexicalPath) {
		lexicalPath = filepath.Join(root, lexicalPath)
	}
	lexicalPath, err = filepath.Abs(lexicalPath)
	if err != nil {
		return "", fmt.Errorf("image generation config error: workflow path rejected: %w", err)
	}
	lexicalPath = filepath.Clean(lexicalPath)
	if err := ensureContained(lexicalPath, root); err != nil {
		return "", fmt.Errorf("image generation config error: workflow path rejected: %w", err)
	}
	if err := ensureNoSymlinkPathComponents(lexicalPath, root, "workflow path"); err != nil {
		return "", err
	}

	info, err := os.Stat(safePath)
	if err != nil {
		return "", fmt.Errorf("image generation config error: failed to stat workflow file: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("image generation config error: workflow path must be a file")
	}
	return safePath, nil
}

func loadWorkflowJSON(workflowPath string) (map[string]interface{}, error) {
	file, err := os.Open(workflowPath)
	if err != nil {
		return nil, fmt.Errorf("image generation config error: failed to read workflow file: %w", err)
	}
	defer file.Close()
	data, err := readAllLimited(file, maxWorkflowJSONBytes, "workflow JSON")
	if err != nil {
		return nil, fmt.Errorf("image generation config error: %w", err)
	}
	var workflow map[string]interface{}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&workflow); err != nil {
		return nil, fmt.Errorf("image generation config error: failed to parse workflow JSON: %w", err)
	}
	if len(workflow) == 0 {
		return nil, fmt.Errorf("image generation config error: workflow JSON is empty")
	}
	return workflow, nil
}

func mutateWorkflowInput(workflow map[string]interface{}, nodeID string, inputName string, value interface{}, fieldName string, required bool) error {
	if strings.TrimSpace(nodeID) == "" {
		if required {
			return fmt.Errorf("image generation config error: %s node ID is required", fieldName)
		}
		return nil
	}
	if strings.TrimSpace(inputName) == "" {
		return fmt.Errorf("image generation config error: %s input name is required when node ID is configured", fieldName)
	}

	node, ok := workflow[nodeID].(map[string]interface{})
	if !ok {
		return fmt.Errorf("image generation config error: workflow node %q for %s was not found", nodeID, fieldName)
	}
	inputs, ok := node["inputs"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("image generation config error: workflow node %q inputs for %s are missing or invalid", nodeID, fieldName)
	}
	inputs[inputName] = value
	return nil
}

func ensureArtifactOutputDir(outputDir string, root string) (string, error) {
	dir, targetAbs, err := openArtifactOutputDir(outputDir, root)
	if err != nil {
		return "", err
	}
	if err := dir.Close(); err != nil {
		return "", fmt.Errorf("image generation config error: failed to close output_dir descriptor: %w", err)
	}
	return targetAbs, nil
}

func openArtifactOutputDir(outputDir string, root string) (*os.File, string, error) {
	outputDir = strings.TrimSpace(outputDir)
	if outputDir == "" {
		return nil, "", fmt.Errorf("image generation config error: output_dir is required")
	}

	fixedBase := filepath.Clean(filepath.Join(root, defaultImageOutputDir))
	if err := ensureContained(fixedBase, root); err != nil {
		return nil, "", fmt.Errorf("image generation config error: fixed image artifact base rejected: %w", err)
	}

	target := outputDir
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return nil, "", fmt.Errorf("image generation config error: output_dir rejected: %w", err)
	}
	targetAbs = filepath.Clean(targetAbs)
	if err := ensureContained(targetAbs, root); err != nil {
		return nil, "", fmt.Errorf("image generation config error: output_dir rejected: %w", err)
	}
	if err := ensureContained(targetAbs, fixedBase); err != nil {
		return nil, "", fmt.Errorf("image generation config error: output_dir must stay under %s: %w", defaultImageOutputDir, err)
	}

	rel, err := filepath.Rel(root, targetAbs)
	if err != nil {
		return nil, "", fmt.Errorf("image generation config error: output_dir rejected: %w", err)
	}

	currentFD, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, "", fmt.Errorf("image generation config error: failed to open workspace root descriptor: %w", err)
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
				return nil, "", formatOpenDirAtError("output_dir", component, err)
			}
			if err := unix.Mkdirat(currentFD, component, 0755); err != nil && err != unix.EEXIST {
				return nil, "", fmt.Errorf("image generation config error: failed to create output_dir component %q: %w", component, err)
			}
			nextFD, err = openDirAtNoFollow(currentFD, component)
			if err != nil {
				return nil, "", formatOpenDirAtError("output_dir", component, err)
			}
		}
		if err := unix.Close(currentFD); err != nil {
			_ = unix.Close(nextFD)
			return nil, "", fmt.Errorf("image generation config error: failed to close output_dir parent descriptor: %w", err)
		}
		currentFD = nextFD
	}

	closeCurrent = false
	return os.NewFile(uintptr(currentFD), targetAbs), targetAbs, nil
}

func openDirAtNoFollow(parentFD int, name string) (int, error) {
	return unix.Openat(parentFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
}

func formatOpenDirAtError(label string, component string, err error) error {
	switch err {
	case unix.ELOOP:
		return fmt.Errorf("image generation config error: %s symlink component is not allowed: %s", label, component)
	case unix.ENOTDIR:
		return fmt.Errorf("image generation config error: %s component is not a directory or is a symlink: %s", label, component)
	default:
		return fmt.Errorf("image generation config error: failed to open %s component %q: %w", label, component, err)
	}
}

func ensureNoSymlinkPathComponents(path string, root string, label string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return fmt.Errorf("image generation config error: %s rejected: %w", label, err)
	}
	if rel == "." {
		return nil
	}

	current := root
	for _, component := range strings.Split(rel, string(os.PathSeparator)) {
		if component == "" || component == "." {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("image generation config error: failed to inspect %s component: %w", label, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("image generation config error: %s symlink component is not allowed: %s", label, current)
		}
	}
	return nil
}

func submitComfyPrompt(ctx context.Context, client *http.Client, endpoint *url.URL, workflow map[string]interface{}) (string, error) {
	reqBody, err := json.Marshal(map[string]interface{}{"prompt": workflow})
	if err != nil {
		return "", fmt.Errorf("failed to encode ComfyUI prompt request: %w", err)
	}

	promptURL := *endpoint
	promptURL.Path = "/prompt"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, promptURL.String(), bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("failed to create ComfyUI prompt request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to submit ComfyUI prompt: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ComfyUI prompt request returned status %d: %s", resp.StatusCode, readLimitedErrorBody(resp.Body))
	}

	var res struct {
		PromptID string `json:"prompt_id"`
	}
	if err := decodeLimitedJSON(resp.Body, maxComfyJSONResponseBytes, "ComfyUI prompt response", &res); err != nil {
		return "", fmt.Errorf("failed to decode ComfyUI prompt response: %w", err)
	}
	if strings.TrimSpace(res.PromptID) == "" {
		return "", fmt.Errorf("ComfyUI prompt response did not include prompt_id")
	}
	return res.PromptID, nil
}

func pollComfyHistory(ctx context.Context, client *http.Client, endpoint *url.URL, promptID string) (comfyImageRef, error) {
	ticker := time.NewTicker(imageHTTPPollInterval)
	defer ticker.Stop()

	for {
		imageRef, found, err := fetchComfyHistoryImage(ctx, client, endpoint, promptID)
		if err != nil {
			return comfyImageRef{}, err
		}
		if found {
			return imageRef, nil
		}

		select {
		case <-ctx.Done():
			return comfyImageRef{}, fmt.Errorf("timed out waiting for ComfyUI image output: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func fetchComfyHistoryImage(ctx context.Context, client *http.Client, endpoint *url.URL, promptID string) (comfyImageRef, bool, error) {
	historyURL := *endpoint
	historyURL.Path = "/history/" + url.PathEscape(promptID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, historyURL.String(), nil)
	if err != nil {
		return comfyImageRef{}, false, fmt.Errorf("failed to create ComfyUI history request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return comfyImageRef{}, false, fmt.Errorf("failed to fetch ComfyUI history: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return comfyImageRef{}, false, fmt.Errorf("ComfyUI history request returned status %d: %s", resp.StatusCode, readLimitedErrorBody(resp.Body))
	}

	var history map[string]comfyHistoryEntry
	if err := decodeLimitedJSON(resp.Body, maxComfyJSONResponseBytes, "ComfyUI history response", &history); err != nil {
		return comfyImageRef{}, false, fmt.Errorf("failed to decode ComfyUI history response: %w", err)
	}
	entry, ok := history[promptID]
	if !ok {
		return comfyImageRef{}, false, nil
	}
	outputIDs := make([]string, 0, len(entry.Outputs))
	for outputID := range entry.Outputs {
		outputIDs = append(outputIDs, outputID)
	}
	sort.Strings(outputIDs)
	for _, outputID := range outputIDs {
		for _, imageRef := range entry.Outputs[outputID].Images {
			if strings.TrimSpace(imageRef.Filename) != "" {
				return imageRef, true, nil
			}
		}
	}
	return comfyImageRef{}, false, nil
}

func downloadComfyImage(ctx context.Context, client *http.Client, endpoint *url.URL, imageRef comfyImageRef, maxBytes int64) ([]byte, string, error) {
	if maxBytes <= 0 {
		maxBytes = defaultImageMaxImageBytes
	}

	viewURL := *endpoint
	viewURL.Path = "/view"
	query := url.Values{}
	query.Set("filename", imageRef.Filename)
	query.Set("subfolder", imageRef.Subfolder)
	query.Set("type", imageRef.Type)
	viewURL.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, viewURL.String(), nil)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create ComfyUI image download request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("failed to download ComfyUI image: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("ComfyUI image download returned status %d: %s", resp.StatusCode, readLimitedErrorBody(resp.Body))
	}
	if resp.ContentLength > maxBytes {
		return nil, "", fmt.Errorf("ComfyUI image exceeds max_image_bytes (%d)", maxBytes)
	}

	limited := io.LimitReader(resp.Body, maxBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read ComfyUI image response: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, "", fmt.Errorf("ComfyUI image exceeds max_image_bytes (%d)", maxBytes)
	}
	canonicalData, ext, err := canonicalizeRasterImage(data, resp.Header.Get("Content-Type"))
	if err != nil {
		return nil, "", err
	}
	return canonicalData, ext, nil
}

func writeImageArtifact(outputDirSetting string, root string, promptID string, remoteFilename string, imageExt string, data []byte) (string, error) {
	remoteSegment := sanitizeArtifactSegment(remoteFilename, "image", maxArtifactNameSegmentRunes)
	remoteBase := strings.TrimSuffix(remoteSegment, filepath.Ext(remoteSegment))
	remoteBase = strings.Trim(remoteBase, "._-")
	if remoteBase == "" {
		remoteBase = "image"
	}
	remoteSegment = remoteBase + imageExt
	promptSegment := sanitizeArtifactSegment(promptID, "prompt", 48)
	timestamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	localName := fmt.Sprintf("%s_%s_%s", timestamp, promptSegment, remoteSegment)

	outputDirFile, outputDir, err := openArtifactOutputDir(outputDirSetting, root)
	if err != nil {
		return "", err
	}
	defer outputDirFile.Close()

	artifactPath := filepath.Join(outputDir, localName)
	artifactPath = filepath.Clean(artifactPath)
	if err := ensureContained(artifactPath, root); err != nil {
		return "", fmt.Errorf("image artifact path rejected: %w", err)
	}
	if strings.ContainsAny(localName, `/\`) {
		return "", fmt.Errorf("image artifact filename rejected")
	}
	outputDirFD := int(outputDirFile.Fd())
	artifactFD, err := unix.Openat(outputDirFD, localName, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return "", fmt.Errorf("failed to create image artifact: %w", err)
	}
	file := os.NewFile(uintptr(artifactFD), artifactPath)
	writeErr := func() (err error) {
		defer func() {
			if closeErr := file.Close(); err == nil && closeErr != nil {
				err = fmt.Errorf("failed to close image artifact: %w", closeErr)
			}
			if err != nil {
				_ = unix.Unlinkat(outputDirFD, localName, 0)
			}
		}()
		n, err := file.Write(data)
		if err != nil {
			return fmt.Errorf("failed to write image artifact: %w", err)
		}
		if n != len(data) {
			return fmt.Errorf("failed to write complete image artifact")
		}
		info, err := file.Stat()
		if err != nil {
			return fmt.Errorf("failed to inspect image artifact descriptor: %w", err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("image artifact descriptor is not a regular file")
		}
		return file.Sync()
	}()
	if writeErr != nil {
		return "", writeErr
	}
	if err := unix.Fsync(outputDirFD); err != nil {
		return "", fmt.Errorf("failed to sync image artifact directory: %w", err)
	}
	return artifactPath, nil
}

func sanitizeArtifactSegment(value string, fallback string, maxRunes int) string {
	var builder strings.Builder
	for _, r := range value {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			builder.WriteRune(r)
		case r == '.', r == '-', r == '_':
			builder.WriteRune(r)
		default:
			builder.WriteByte('_')
		}
	}
	sanitized := strings.Trim(builder.String(), "._-")
	if sanitized == "" {
		sanitized = fallback
	}
	runes := []rune(sanitized)
	if len(runes) > maxRunes {
		ext := filepath.Ext(sanitized)
		if ext != "" && len([]rune(ext)) < maxRunes {
			baseRunes := []rune(strings.TrimSuffix(sanitized, ext))
			keep := maxRunes - len([]rune(ext))
			if len(baseRunes) > keep {
				baseRunes = baseRunes[:keep]
			}
			sanitized = string(baseRunes) + ext
		} else {
			sanitized = string(runes[:maxRunes])
		}
	}
	return sanitized
}

func canonicalizeRasterImage(data []byte, contentType string) ([]byte, string, error) {
	if len(data) == 0 {
		return nil, "", fmt.Errorf("ComfyUI image response is empty")
	}

	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType == "" || mediaType == "application/octet-stream" {
		mediaType = http.DetectContentType(data)
	}
	ext := imageExtensionForMediaType(mediaType)
	if ext == "" {
		return nil, "", fmt.Errorf("ComfyUI image content type %q is not an allowed raster image type", mediaType)
	}

	detectedType := http.DetectContentType(data)
	detectedExt := imageExtensionForMediaType(detectedType)
	if detectedExt == "" || detectedExt != ext {
		return nil, "", fmt.Errorf("ComfyUI image content does not match allowed image type %q", mediaType)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("ComfyUI image content is not a valid raster image: %w", err)
	}
	if imageExtensionForFormat(format) != ext {
		return nil, "", fmt.Errorf("ComfyUI image decoder format %q does not match content type %q", format, mediaType)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxImageDimension || cfg.Height > maxImageDimension {
		return nil, "", fmt.Errorf("ComfyUI image dimensions %dx%d exceed allowed bounds", cfg.Width, cfg.Height)
	}
	if int64(cfg.Width)*int64(cfg.Height) > int64(maxImageDimension)*int64(maxImageDimension) {
		return nil, "", fmt.Errorf("ComfyUI image pixel count exceeds allowed bounds")
	}

	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("ComfyUI image content is not fully decodable: %w", err)
	}
	if imageExtensionForFormat(format) != ext {
		return nil, "", fmt.Errorf("ComfyUI image decoder format %q changed during full decode", format)
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, "", fmt.Errorf("failed to canonicalize ComfyUI image as PNG: %w", err)
	}
	return buf.Bytes(), ".png", nil
}

func imageExtensionForMediaType(mediaType string) string {
	mediaType, _, err := mime.ParseMediaType(mediaType)
	if err != nil {
		return ""
	}
	switch mediaType {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	default:
		return ""
	}
}

func imageExtensionForFormat(format string) string {
	switch format {
	case "png":
		return ".png"
	case "jpeg":
		return ".jpg"
	case "gif":
		return ".gif"
	default:
		return ""
	}
}

func decodeLimitedJSON(body io.Reader, maxBytes int64, label string, target interface{}) error {
	data, err := readAllLimited(body, maxBytes, label)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return decoder.Decode(target)
}

func readAllLimited(body io.Reader, maxBytes int64, label string) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("%s exceeds %d bytes", label, maxBytes)
	}
	return data, nil
}

func readLimitedErrorBody(body io.Reader) string {
	data, err := io.ReadAll(io.LimitReader(body, 1024))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
