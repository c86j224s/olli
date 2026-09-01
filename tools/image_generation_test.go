package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/c86j224s/olli/ollama"
)

func TestImageGenerateRejectsUnsafeEndpointsTempOnlyNoNetwork(t *testing.T) {
	// Blast radius if the guard regresses: temp/repo-local config validation only; no HTTP server is contacted.
	root := testSafeTempRoot(t)
	unsafeEndpoints := []string{
		"https://127.0.0.1:8188",
		"http://example.com:8188",
		"http://user@127.0.0.1:8188",
		"http://127.0.0.1:8188/path",
		"http://127.0.0.1:8188?x=1",
		"http://127.0.0.1:8188#fragment",
	}

	for _, endpoint := range unsafeEndpoints {
		reg := NewRegistry()
		reg.SetWorkspaceRoot(root)
		reg.SetWorkspace(root)
		reg.SetImageGenerationConfig(ImageGenerationConfig{
			ComfyUI: ComfyUIConfig{
				Endpoint: endpoint,
				Workflows: map[string]ComfyUIWorkflowConfig{
					"default": {Path: "workflow.json"},
				},
			},
		})

		_, err := reg.ExecuteContext(mediaTestContext(reg), "image_generate", map[string]interface{}{
			"backend_alias":  "comfyui",
			"workflow_alias": "default",
			"prompt":         "test prompt",
		})
		if err == nil || !strings.Contains(err.Error(), "endpoint") {
			t.Fatalf("expected endpoint rejection for %q, got: %v", endpoint, err)
		}
	}
}

func TestImageGenerateMissingWorkflowTempOnlyNoNetwork(t *testing.T) {
	// Blast radius if the guard regresses: temp/repo-local config validation only; no workflow file or HTTP call is used.
	root := testSafeTempRoot(t)
	reg := NewRegistry()
	reg.SetWorkspaceRoot(root)
	reg.SetWorkspace(root)

	_, err := reg.ExecuteContext(mediaTestContext(reg), "image_generate", map[string]interface{}{
		"backend_alias":  "comfyui",
		"workflow_alias": "default",
		"prompt":         "test prompt",
	})
	if err == nil || !strings.Contains(err.Error(), "no ComfyUI workflows configured") {
		t.Fatalf("expected missing workflow config error, got: %v", err)
	}
}

func TestImageGenerateToolExposesConfiguredWorkflowAliases(t *testing.T) {
	reg := NewRegistry()
	reg.SetImageGenerationConfig(ImageGenerationConfig{
		ComfyUI: ComfyUIConfig{
			Workflows: map[string]ComfyUIWorkflowConfig{
				"zeta":  {Path: "zeta.json"},
				"alpha": {Path: "alpha.json"},
			},
		},
	})

	var workflowProperty *ollama.FunctionParamProperty
	for _, definition := range reg.GetDefinitions() {
		if definition.Function.Name != imageGenerateToolName {
			continue
		}
		property := definition.Function.Parameters.Properties["workflow_alias"]
		workflowProperty = &property
		break
	}
	if workflowProperty == nil {
		t.Fatal("expected image_generate workflow_alias schema property")
	}
	if len(workflowProperty.Enum) != 2 || workflowProperty.Enum[0] != "alpha" || workflowProperty.Enum[1] != "zeta" {
		t.Fatalf("expected sorted configured aliases in schema, got %v", workflowProperty.Enum)
	}
	if !strings.Contains(workflowProperty.Description, "alpha, zeta") {
		t.Fatalf("expected configured aliases in description, got %q", workflowProperty.Description)
	}
}

func TestImageWorkflowAliasArgDefaultsOnlyConfiguredAlias(t *testing.T) {
	workflows := map[string]ComfyUIWorkflowConfig{
		"default_workflow": {Path: "workflow.json"},
	}

	alias, err := imageWorkflowAliasArg(map[string]interface{}{}, workflows)
	if err != nil {
		t.Fatalf("expected single configured alias to be selected: %v", err)
	}
	if alias != "default_workflow" {
		t.Fatalf("expected default_workflow, got %q", alias)
	}
}

func TestImageWorkflowAliasArgRequiresChoiceForMultipleAliases(t *testing.T) {
	workflows := map[string]ComfyUIWorkflowConfig{
		"zeta":  {Path: "zeta.json"},
		"alpha": {Path: "alpha.json"},
	}

	_, err := imageWorkflowAliasArg(map[string]interface{}{}, workflows)
	if err == nil || !strings.Contains(err.Error(), "alpha, zeta") {
		t.Fatalf("expected sorted alias choice error, got: %v", err)
	}
}

func TestImageGenerateRejectsSymlinkWorkflowFileTempOnlyNoNetwork(t *testing.T) {
	// Blast radius if the guard regresses: temp/repo-local workflow validation only; no HTTP server is contacted.
	root := testSafeTempRoot(t)
	realWorkflow := filepath.Join(root, "real-workflow.json")
	if err := os.WriteFile(realWorkflow, []byte(testComfyWorkflowJSON()), 0600); err != nil {
		t.Fatalf("failed to write real workflow fixture: %v", err)
	}
	if err := os.Symlink(realWorkflow, filepath.Join(root, "workflow-link.json")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}

	reg := NewRegistry()
	reg.SetWorkspaceRoot(root)
	reg.SetWorkspace(root)
	reg.SetImageGenerationConfig(ImageGenerationConfig{
		ComfyUI: ComfyUIConfig{
			Endpoint: "http://127.0.0.1:8188",
			Workflows: map[string]ComfyUIWorkflowConfig{
				"default": {Path: "workflow-link.json"},
			},
		},
	})

	_, err := reg.ExecuteContext(mediaTestContext(reg), "image_generate", map[string]interface{}{
		"backend_alias":  "comfyui",
		"workflow_alias": "default",
		"prompt":         "test prompt",
	})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected workflow symlink rejection, got: %v", err)
	}
}

func TestImageGenerateRejectsRedirectsTempOnlyHttptest(t *testing.T) {
	// Blast radius if the guard regresses: temp workflow only; redirect target is another httptest server.
	root := testSafeTempRoot(t)
	workflowPath := filepath.Join(root, "workflow.json")
	if err := os.WriteFile(workflowPath, []byte(testComfyWorkflowJSON()), 0600); err != nil {
		t.Fatalf("failed to write workflow fixture: %v", err)
	}

	var redirectedHits int
	redirectTarget := newInProcessTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectedHits++
		http.Error(w, "redirect target must not be reached", http.StatusInternalServerError)
	}))
	defer redirectTarget.Close()

	server := newInProcessTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectTarget.URL+"/leak", http.StatusTemporaryRedirect)
	}))
	defer server.Close()

	reg := NewRegistry()
	reg.SetWorkspaceRoot(root)
	reg.SetWorkspace(root)
	reg.SetImageGenerationConfig(ImageGenerationConfig{
		ComfyUI: ComfyUIConfig{
			Endpoint:       server.URL,
			OutputDir:      "artifacts/images",
			TimeoutSeconds: 2,
			MaxImageBytes:  1024,
			Workflows: map[string]ComfyUIWorkflowConfig{
				"default": testComfyWorkflowConfig("workflow.json"),
			},
		},
	})

	_, err := reg.ExecuteContext(mediaTestContext(reg, server.Config.Handler), "image_generate", map[string]interface{}{
		"backend_alias":  "comfyui",
		"workflow_alias": "default",
		"prompt":         "test prompt",
	})
	if err == nil || !strings.Contains(err.Error(), "status 307") {
		t.Fatalf("expected redirect status rejection, got: %v", err)
	}
	if redirectedHits != 0 {
		t.Fatalf("redirect target was reached %d times", redirectedHits)
	}
}

func TestImageGenerateComfyUISuccessPathTempOnlyHttptest(t *testing.T) {
	// Blast radius if the guard regresses: temp workspace artifacts only; all ComfyUI calls use httptest.
	root := testSafeTempRoot(t)
	workflowPath := filepath.Join(root, "workflow.json")
	if err := os.WriteFile(workflowPath, []byte(testComfyWorkflowJSON()), 0600); err != nil {
		t.Fatalf("failed to write workflow fixture: %v", err)
	}

	imageBytes := testPNGBytes(t)
	var sawPrompt bool
	var sawHistory bool
	var sawView bool
	var serverMu sync.Mutex
	var serverErrors []string
	recordServerError := func(w http.ResponseWriter, format string, args ...interface{}) {
		msg := fmt.Sprintf(format, args...)
		serverMu.Lock()
		serverErrors = append(serverErrors, msg)
		serverMu.Unlock()
		http.Error(w, msg, http.StatusInternalServerError)
	}
	server := newInProcessTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/prompt":
			if r.Method != http.MethodPost {
				recordServerError(w, "expected POST /prompt, got %s", r.Method)
				return
			}
			var req struct {
				Prompt map[string]struct {
					Inputs map[string]interface{} `json:"inputs"`
				} `json:"prompt"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				recordServerError(w, "failed to decode prompt request: %v", err)
				return
			}
			if req.Prompt["6"].Inputs["text"] != "sunlit workspace" {
				recordServerError(w, "prompt was not mutated: %#v", req.Prompt["6"].Inputs["text"])
				return
			}
			if req.Prompt["7"].Inputs["text"] != "blur" {
				recordServerError(w, "negative prompt was not mutated: %#v", req.Prompt["7"].Inputs["text"])
				return
			}
			if req.Prompt["5"].Inputs["width"] != float64(640) {
				recordServerError(w, "width was not mutated: %#v", req.Prompt["5"].Inputs["width"])
				return
			}
			if req.Prompt["5"].Inputs["height"] != float64(512) {
				recordServerError(w, "height was not mutated: %#v", req.Prompt["5"].Inputs["height"])
				return
			}
			if req.Prompt["3"].Inputs["steps"] != float64(11) {
				recordServerError(w, "steps were not mutated: %#v", req.Prompt["3"].Inputs["steps"])
				return
			}
			if req.Prompt["3"].Inputs["seed"] != float64(12345) {
				recordServerError(w, "seed was not mutated: %#v", req.Prompt["3"].Inputs["seed"])
				return
			}
			sawPrompt = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"prompt_id":"prompt-123"}`))
		case "/history/prompt-123":
			sawHistory = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"prompt-123":{"outputs":{"9":{"images":[{"filename":"folder/unsafe name.go","subfolder":"nested/out","type":"output"}]}}}}`))
		case "/view":
			if r.URL.Query().Get("filename") != "folder/unsafe name.go" {
				recordServerError(w, "filename query changed: %q", r.URL.Query().Get("filename"))
				return
			}
			if r.URL.Query().Get("subfolder") != "nested/out" {
				recordServerError(w, "subfolder query changed: %q", r.URL.Query().Get("subfolder"))
				return
			}
			if r.URL.Query().Get("type") != "output" {
				recordServerError(w, "type query changed: %q", r.URL.Query().Get("type"))
				return
			}
			sawView = true
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(imageBytes)
		default:
			recordServerError(w, "unexpected ComfyUI path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	reg := NewRegistry()
	reg.SetWorkspaceRoot(root)
	reg.SetWorkspace(root)
	reg.SetImageGenerationConfig(ImageGenerationConfig{
		ComfyUI: ComfyUIConfig{
			Endpoint:       server.URL,
			OutputDir:      "artifacts/images",
			TimeoutSeconds: 2,
			MaxImageBytes:  1024,
			Workflows: map[string]ComfyUIWorkflowConfig{
				"default": testComfyWorkflowConfig("workflow.json"),
			},
		},
	})

	resultJSON, err := reg.ExecuteContext(mediaTestContext(reg, server.Config.Handler), "image_generate", map[string]interface{}{
		"backend_alias":   "comfyui",
		"prompt":          "sunlit workspace",
		"negative_prompt": "blur",
		"width":           640,
		"height":          512,
		"steps":           11,
		"seed":            12345,
	})
	if err != nil {
		serverMu.Lock()
		defer serverMu.Unlock()
		if len(serverErrors) > 0 {
			t.Fatalf("image_generate failed after server assertion: %v; server errors: %s", err, strings.Join(serverErrors, "; "))
		}
		t.Fatalf("image_generate failed: %v", err)
	}
	serverMu.Lock()
	if len(serverErrors) > 0 {
		t.Fatalf("server errors: %s", strings.Join(serverErrors, "; "))
	}
	serverMu.Unlock()
	if !sawPrompt || !sawHistory || !sawView {
		t.Fatalf("expected prompt/history/view calls, got prompt=%v history=%v view=%v", sawPrompt, sawHistory, sawView)
	}

	var result imageGenerateResult
	if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
		t.Fatalf("failed to decode result JSON: %v\n%s", err, resultJSON)
	}
	if err := ensureContained(result.ArtifactPath, root); err != nil {
		t.Fatalf("artifact escaped workspace root: %v", err)
	}
	if !strings.HasPrefix(result.WorkspaceRelativePath, filepath.Join("artifacts", "images")+string(os.PathSeparator)) {
		t.Fatalf("unexpected relative artifact path: %s", result.WorkspaceRelativePath)
	}
	if !strings.Contains(filepath.Base(result.ArtifactPath), "folder_unsafe_name.png") {
		t.Fatalf("returned filename was not sanitized into local artifact name: %s", result.ArtifactPath)
	}
	if strings.HasSuffix(result.ArtifactPath, ".go") {
		t.Fatalf("remote executable extension was preserved: %s", result.ArtifactPath)
	}
	written, err := os.ReadFile(result.ArtifactPath)
	if err != nil {
		t.Fatalf("failed to read artifact: %v", err)
	}
	if _, _, err := image.DecodeConfig(bytes.NewReader(written)); err != nil {
		t.Fatalf("artifact is not a valid image: %v", err)
	}
}

func TestCanonicalizeRasterImageStripsTrailingPayloadTempOnly(t *testing.T) {
	// Blast radius if the guard regresses: in-memory image validation only; no filesystem or HTTP mutation.
	payload := []byte("package payload\n")
	imageWithPayload := append(testPNGBytes(t), payload...)

	canonical, ext, err := canonicalizeRasterImage(imageWithPayload, "image/png")
	if err != nil {
		t.Fatalf("canonicalizeRasterImage failed: %v", err)
	}
	if ext != ".png" {
		t.Fatalf("expected canonical PNG extension, got %s", ext)
	}
	if bytes.Contains(canonical, payload) {
		t.Fatal("canonical image retained trailing payload")
	}
	if bytes.Equal(canonical, imageWithPayload) {
		t.Fatal("canonical image still equals payload-bearing input")
	}
	if _, _, err := image.DecodeConfig(bytes.NewReader(canonical)); err != nil {
		t.Fatalf("canonical artifact is not a valid image: %v", err)
	}
}

func TestImageGenerateRejectsInvalidImageBytesTempOnlyHttptest(t *testing.T) {
	// Blast radius if the guard regresses: temp workspace artifacts only; all ComfyUI calls use httptest.
	root := testSafeTempRoot(t)
	workflowPath := filepath.Join(root, "workflow.json")
	if err := os.WriteFile(workflowPath, []byte(testComfyWorkflowJSON()), 0600); err != nil {
		t.Fatalf("failed to write workflow fixture: %v", err)
	}

	server := newInProcessTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/prompt":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"prompt_id":"prompt-123"}`))
		case "/history/prompt-123":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"prompt-123":{"outputs":{"9":{"images":[{"filename":"payload.go","subfolder":"","type":"output"}]}}}}`))
		case "/view":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("package payload\n"))
		default:
			http.Error(w, "unexpected path", http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	reg := NewRegistry()
	reg.SetWorkspaceRoot(root)
	reg.SetWorkspace(root)
	reg.SetImageGenerationConfig(ImageGenerationConfig{
		ComfyUI: ComfyUIConfig{
			Endpoint:       server.URL,
			OutputDir:      "artifacts/images",
			TimeoutSeconds: 2,
			MaxImageBytes:  1024,
			Workflows: map[string]ComfyUIWorkflowConfig{
				"default": testComfyWorkflowConfig("workflow.json"),
			},
		},
	})

	_, err := reg.ExecuteContext(mediaTestContext(reg, server.Config.Handler), "image_generate", map[string]interface{}{
		"backend_alias":  "comfyui",
		"workflow_alias": "default",
		"prompt":         "test prompt",
	})
	if err == nil || !strings.Contains(err.Error(), "allowed image type") {
		t.Fatalf("expected invalid image rejection, got: %v", err)
	}
	entries, readErr := os.ReadDir(filepath.Join(root, "artifacts", "images"))
	if readErr != nil {
		t.Fatalf("failed to inspect artifact dir: %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no artifact files after invalid image, got %d", len(entries))
	}
}

func TestImageGenerateRejectsSymlinkOutputComponentTempOnlyNoHTTP(t *testing.T) {
	// Blast radius if the guard regresses: temp workspace and temp outside marker only; HTTP must not be reached.
	root := testSafeTempRoot(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "artifacts")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	workflowPath := filepath.Join(root, "workflow.json")
	if err := os.WriteFile(workflowPath, []byte(testComfyWorkflowJSON()), 0600); err != nil {
		t.Fatalf("failed to write workflow fixture: %v", err)
	}

	var httpHits int
	server := newInProcessTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpHits++
		http.Error(w, "HTTP should not be reached when output_dir has a symlink component", http.StatusInternalServerError)
	}))
	defer server.Close()

	reg := NewRegistry()
	reg.SetWorkspaceRoot(root)
	reg.SetWorkspace(root)
	reg.SetImageGenerationConfig(ImageGenerationConfig{
		ComfyUI: ComfyUIConfig{
			Endpoint:       server.URL,
			OutputDir:      "artifacts/images",
			TimeoutSeconds: 2,
			MaxImageBytes:  1024,
			Workflows: map[string]ComfyUIWorkflowConfig{
				"default": testComfyWorkflowConfig("workflow.json"),
			},
		},
	})

	_, err := reg.ExecuteContext(mediaTestContext(reg), "image_generate", map[string]interface{}{
		"backend_alias":  "comfyui",
		"workflow_alias": "default",
		"prompt":         "test prompt",
	})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink output_dir rejection, got: %v", err)
	}
	if httpHits != 0 {
		t.Fatalf("expected no HTTP calls before symlink rejection, got %d", httpHits)
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatalf("failed to inspect outside temp dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("outside temp dir was mutated: %d entries", len(entries))
	}
}

func TestImageGenerateRejectsOutputDirsOutsideArtifactBaseTempOnly(t *testing.T) {
	// Blast radius if the guard regresses: temp/repo-local output_dir validation only.
	root := testSafeTempRoot(t)
	outside := t.TempDir()
	rejected := []string{
		".",
		"agent",
		"artifacts",
		filepath.Join("artifacts", "..", "agent"),
		filepath.Join(root, "agent"),
		filepath.Join(outside, "images"),
	}

	for _, outputDir := range rejected {
		if _, err := ensureArtifactOutputDir(outputDir, root); err == nil {
			t.Fatalf("expected output_dir %q to be rejected", outputDir)
		}
	}
	if _, err := ensureArtifactOutputDir(filepath.Join("artifacts", "images"), root); err != nil {
		t.Fatalf("expected default artifact output dir to pass: %v", err)
	}
	if _, err := ensureArtifactOutputDir(filepath.Join("artifacts", "images", "run1"), root); err != nil {
		t.Fatalf("expected artifact subdir output dir to pass: %v", err)
	}
}

func TestImageGenerateRejectsUnsafeResourceBoundsTempOnlyNoNetwork(t *testing.T) {
	// Blast radius if the guard regresses: temp/repo-local argument validation only; no workflow file or HTTP call is used.
	root := testSafeTempRoot(t)
	cases := []map[string]interface{}{
		{"width": maxImageDimension + 1},
		{"height": maxImageDimension + 1},
		{"steps": maxImageSteps + 1},
		{"seed": -1},
	}

	for _, extraArgs := range cases {
		reg := NewRegistry()
		reg.SetWorkspaceRoot(root)
		reg.SetWorkspace(root)
		reg.SetImageGenerationConfig(ImageGenerationConfig{
			ComfyUI: ComfyUIConfig{
				Endpoint: "http://127.0.0.1:8188",
				Workflows: map[string]ComfyUIWorkflowConfig{
					"default": {Path: "workflow.json", PromptNodeID: "6", PromptInput: "text"},
				},
			},
		})
		args := map[string]interface{}{
			"backend_alias":  "comfyui",
			"workflow_alias": "default",
			"prompt":         "test prompt",
		}
		for key, value := range extraArgs {
			args[key] = value
		}
		if _, err := reg.ExecuteContext(mediaTestContext(reg), "image_generate", args); err == nil {
			t.Fatalf("expected resource bounds rejection for args %#v", extraArgs)
		}
	}
}

func TestImageGenerateRequiresPromptMappingTempOnlyNoNetwork(t *testing.T) {
	// Blast radius if the guard regresses: temp/repo-local workflow validation only; no HTTP server is contacted.
	root := testSafeTempRoot(t)
	workflowPath := filepath.Join(root, "workflow.json")
	if err := os.WriteFile(workflowPath, []byte(testComfyWorkflowJSON()), 0600); err != nil {
		t.Fatalf("failed to write workflow fixture: %v", err)
	}

	reg := NewRegistry()
	reg.SetWorkspaceRoot(root)
	reg.SetWorkspace(root)
	reg.SetImageGenerationConfig(ImageGenerationConfig{
		ComfyUI: ComfyUIConfig{
			Endpoint:       "http://127.0.0.1:8188",
			TimeoutSeconds: 2,
			MaxImageBytes:  1024,
			Workflows: map[string]ComfyUIWorkflowConfig{
				"default": {Path: "workflow.json"},
			},
		},
	})

	_, err := reg.ExecuteContext(mediaTestContext(reg), "image_generate", map[string]interface{}{
		"backend_alias":  "comfyui",
		"workflow_alias": "default",
		"prompt":         "test prompt",
	})
	if err == nil || !strings.Contains(err.Error(), "prompt node ID is required") {
		t.Fatalf("expected missing prompt mapping rejection, got: %v", err)
	}
}

func TestLoadWorkflowJSONPreservesLargeIntegersTempOnly(t *testing.T) {
	// Blast radius if the guard regresses: temp/repo-local workflow parsing only.
	root := testSafeTempRoot(t)
	workflowPath := filepath.Join(root, "workflow.json")
	largeSeed := "9007199254740993"
	if err := os.WriteFile(workflowPath, []byte(`{"3":{"inputs":{"seed":`+largeSeed+`}}}`), 0600); err != nil {
		t.Fatalf("failed to write workflow fixture: %v", err)
	}

	workflow, err := loadWorkflowJSON(workflowPath)
	if err != nil {
		t.Fatalf("failed to load workflow: %v", err)
	}
	node := workflow["3"].(map[string]interface{})
	inputs := node["inputs"].(map[string]interface{})
	seed, ok := inputs["seed"].(json.Number)
	if !ok {
		t.Fatalf("expected json.Number seed, got %T", inputs["seed"])
	}
	if seed.String() != largeSeed {
		t.Fatalf("large seed was changed: got %s want %s", seed.String(), largeSeed)
	}
}

func testComfyWorkflowConfig(path string) ComfyUIWorkflowConfig {
	return ComfyUIWorkflowConfig{
		Path:                 path,
		PromptNodeID:         "6",
		PromptInput:          "text",
		NegativePromptNodeID: "7",
		NegativePromptInput:  "text",
		WidthNodeID:          "5",
		WidthInput:           "width",
		HeightNodeID:         "5",
		HeightInput:          "height",
		StepsNodeID:          "3",
		StepsInput:           "steps",
		SeedNodeID:           "3",
		SeedInput:            "seed",
	}
}

func testSafeTempRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	safeRoot, err := IsPathSafeFrom(".", root, root)
	if err != nil {
		t.Fatalf("failed to canonicalize temp root: %v", err)
	}
	return safeRoot
}

func testComfyWorkflowJSON() string {
	return `{
  "3": {"inputs": {"seed": 1, "steps": 20}},
  "5": {"inputs": {"width": 1024, "height": 1024}},
  "6": {"inputs": {"text": "old prompt"}},
  "7": {"inputs": {"text": "old negative"}}
}`
}

func testPNGBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("failed to encode PNG fixture: %v", err)
	}
	return buf.Bytes()
}
