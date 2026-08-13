package tools

import (
	"encoding/base64"
	"encoding/json"
	"image"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/c86j224s/olli/ollama"
)

func TestImageInspectRejectsUnsafeEndpointsTempOnlyNoNetwork(t *testing.T) {
	// Blast radius if the guard regresses: temp/repo-local config validation only; no image file or HTTP server is contacted.
	unsafeEndpoints := []string{
		"https://127.0.0.1:11434",
		"http://example.com:11434",
		"http://user@127.0.0.1:11434",
		"http://127.0.0.1:11434/path",
		"http://127.0.0.1:11434?x=1",
		"http://127.0.0.1:11434#fragment",
	}
	for _, endpoint := range unsafeEndpoints {
		reg := NewRegistry()
		reg.SetImageInspectionConfig(ImageInspectionConfig{
			Ollama: OllamaImageInspectionConfig{Endpoint: endpoint, Model: "vision-test"},
		})
		_, err := reg.Execute("inspect_image", map[string]interface{}{"path": "artifacts/images/generated.png"})
		if err == nil || !strings.Contains(err.Error(), "endpoint") {
			t.Fatalf("expected endpoint rejection for %q, got: %v", endpoint, err)
		}
	}
}

func TestDecodeImageInspectionAssessment(t *testing.T) {
	const validAssessment = `{"description":"A red pixel.","answer":"The image is visible.","quality_issues":[],"uncertainties":[],"confidence":0}`

	tests := []struct {
		name       string
		content    string
		wantErr    string
		wantResult imageInspectionAssessment
	}{
		{
			name:    "all fields including explicit zero confidence and empty arrays",
			content: validAssessment,
			wantResult: imageInspectionAssessment{
				Description:   "A red pixel.",
				Answer:        "The image is visible.",
				QualityIssues: []string{},
				Uncertainties: []string{},
				Confidence:    0,
			},
		},
		{name: "omitted description", content: `{"answer":"visible","quality_issues":[],"uncertainties":[],"confidence":0.5}`, wantErr: "omitted required assessment fields"},
		{name: "omitted answer", content: `{"description":"image","quality_issues":[],"uncertainties":[],"confidence":0.5}`, wantErr: "omitted required assessment fields"},
		{name: "omitted quality issues", content: `{"description":"image","answer":"visible","uncertainties":[],"confidence":0.5}`, wantErr: "omitted required assessment fields"},
		{name: "omitted uncertainties", content: `{"description":"image","answer":"visible","quality_issues":[],"confidence":0.5}`, wantErr: "omitted required assessment fields"},
		{name: "omitted confidence", content: `{"description":"image","answer":"visible","quality_issues":[],"uncertainties":[]}`, wantErr: "omitted required assessment fields"},
		{name: "unknown field", content: validAssessment[:len(validAssessment)-1] + `,"extra":true}`, wantErr: "not valid structured JSON"},
		{name: "trailing JSON", content: validAssessment + ` {}`, wantErr: "not valid structured JSON"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assessment, err := decodeImageInspectionAssessment(tt.content)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("decodeImageInspectionAssessment failed: %v", err)
				}
				if assessment.Description != tt.wantResult.Description || assessment.Answer != tt.wantResult.Answer || assessment.Confidence != tt.wantResult.Confidence {
					t.Fatalf("unexpected assessment: %#v", assessment)
				}
				if tt.wantResult.QualityIssues == nil || assessment.QualityIssues == nil || len(assessment.QualityIssues) != 0 {
					t.Fatalf("expected empty quality issues, got %#v", assessment.QualityIssues)
				}
				if tt.wantResult.Uncertainties == nil || assessment.Uncertainties == nil || len(assessment.Uncertainties) != 0 {
					t.Fatalf("expected empty uncertainties, got %#v", assessment.Uncertainties)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got: %v", tt.wantErr, err)
			}
		})
	}
}

func TestImageInspectSuccessPathTempOnlyHttptest(t *testing.T) {
	root := testSafeTempRoot(t)
	imageDir := filepath.Join(root, "artifacts", "images")
	if err := os.MkdirAll(imageDir, 0755); err != nil {
		t.Fatalf("failed to create image artifact directory: %v", err)
	}
	imagePath := filepath.Join(imageDir, "generated.png")
	if err := os.WriteFile(imagePath, testPNGBytes(t), 0600); err != nil {
		t.Fatalf("failed to write image fixture: %v", err)
	}

	var sawShow bool
	var sawChat bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/show":
			sawShow = true
			var request map[string]string
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("failed to decode model request: %v", err)
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			if request["model"] != "vision-test" {
				t.Errorf("unexpected model request: %q", request["model"])
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"capabilities":["completion","vision"]}`))
		case "/api/chat":
			sawChat = true
			var request ollama.ChatRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("failed to decode chat request: %v", err)
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			if request.Stream {
				t.Error("expected non-streaming image inspection request")
			}
			if request.Model != "vision-test" {
				t.Errorf("unexpected chat model: %q", request.Model)
			}
			if request.Format == nil {
				t.Error("expected structured output schema")
			}
			if len(request.Messages) != 2 || len(request.Messages[1].Images) != 1 {
				t.Fatalf("expected one image in user message, got %#v", request.Messages)
			}
			decoded, err := base64.StdEncoding.DecodeString(request.Messages[1].Images[0])
			if err != nil {
				t.Fatalf("image was not base64 encoded: %v", err)
			}
			if _, _, err := image.DecodeConfig(strings.NewReader(string(decoded))); err != nil {
				t.Fatalf("encoded payload was not a valid image: %v", err)
			}
			content := `{"description":"A red pixel.","answer":"The image is visible.","quality_issues":[],"uncertainties":["The fixture is minimal."],"confidence":0.75}`
			response := ollama.ChatResponseChunk{
				Message: ollama.Message{Role: "assistant", Content: content},
				Done:    true,
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
		default:
			http.Error(w, "unexpected path", http.StatusNotFound)
		}
	}))
	defer server.Close()

	reg := NewRegistry()
	reg.SetWorkspaceRoot(root)
	reg.SetWorkspace(root)
	reg.SetImageInspectionConfig(ImageInspectionConfig{
		Ollama: OllamaImageInspectionConfig{
			Endpoint:       server.URL,
			Model:          "vision-test",
			TimeoutSeconds: 2,
			MaxImageBytes:  1024,
		},
	})

	resultJSON, err := reg.Execute("inspect_image", map[string]interface{}{
		"path":     "artifacts/images/generated.png",
		"question": "What is visible?",
	})
	if err != nil {
		t.Fatalf("inspect_image failed: %v", err)
	}
	if !sawShow || !sawChat {
		t.Fatalf("expected show and chat requests, got show=%v chat=%v", sawShow, sawChat)
	}
	var result imageInspectionResult
	if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
		t.Fatalf("failed to decode result: %v", err)
	}
	if result.Path != filepath.Join("artifacts", "images", "generated.png") {
		t.Fatalf("unexpected result path: %q", result.Path)
	}
	if result.Width != 1 || result.Height != 1 || result.MediaType != "image/png" {
		t.Fatalf("unexpected image metadata: %dx%d %s", result.Width, result.Height, result.MediaType)
	}
	if result.Assessment.Confidence != 0.75 || result.Assessment.Answer != "The image is visible." {
		t.Fatalf("unexpected assessment: %#v", result.Assessment)
	}
	if !strings.Contains(result.Disclaimer, "not ground truth") {
		t.Fatalf("expected assessment disclaimer, got %q", result.Disclaimer)
	}
}

func TestImageInspectRejectsOutsideArtifactRootTempOnlyNoNetwork(t *testing.T) {
	// Blast radius if the guard regresses: temp workspace reads only; no network call is made.
	root := testSafeTempRoot(t)
	if err := os.MkdirAll(filepath.Join(root, "artifacts", "images"), 0755); err != nil {
		t.Fatalf("failed to create artifact directory: %v", err)
	}
	outsidePath := filepath.Join(root, "outside.png")
	if err := os.WriteFile(outsidePath, testPNGBytes(t), 0600); err != nil {
		t.Fatalf("failed to write outside fixture: %v", err)
	}

	reg := NewRegistry()
	reg.SetWorkspaceRoot(root)
	reg.SetWorkspace(root)
	_, err := reg.Execute("inspect_image", map[string]interface{}{"path": "outside.png"})
	if err == nil || !strings.Contains(err.Error(), "artifacts/images") {
		t.Fatalf("expected artifact containment rejection, got: %v", err)
	}
}

func TestImageInspectRejectsSymlinkTempOnlyNoNetwork(t *testing.T) {
	// Blast radius if the guard regresses: temp workspace symlink reads only; no network call is made.
	root := testSafeTempRoot(t)
	imageDir := filepath.Join(root, "artifacts", "images")
	if err := os.MkdirAll(imageDir, 0755); err != nil {
		t.Fatalf("failed to create artifact directory: %v", err)
	}
	realPath := filepath.Join(imageDir, "real.png")
	if err := os.WriteFile(realPath, testPNGBytes(t), 0600); err != nil {
		t.Fatalf("failed to write real fixture: %v", err)
	}
	if err := os.Symlink(realPath, filepath.Join(imageDir, "link.png")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}

	reg := NewRegistry()
	reg.SetWorkspaceRoot(root)
	reg.SetWorkspace(root)
	_, err := reg.Execute("inspect_image", map[string]interface{}{"path": "artifacts/images/link.png"})
	if err == nil {
		t.Fatal("expected symlink image to be rejected")
	}
}

func TestImageInspectRejectsNonVisionModelTempOnlyHttptest(t *testing.T) {
	// Blast radius if the guard regresses: a temp workspace image and loopback httptest server only.
	root := testSafeTempRoot(t)
	imageDir := filepath.Join(root, "artifacts", "images")
	if err := os.MkdirAll(imageDir, 0755); err != nil {
		t.Fatalf("failed to create artifact directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(imageDir, "generated.png"), testPNGBytes(t), 0600); err != nil {
		t.Fatalf("failed to write image fixture: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/show" {
			t.Errorf("unexpected request path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"capabilities":["completion"]}`))
	}))
	defer server.Close()

	reg := NewRegistry()
	reg.SetWorkspaceRoot(root)
	reg.SetWorkspace(root)
	reg.SetImageInspectionConfig(ImageInspectionConfig{
		Ollama: OllamaImageInspectionConfig{Endpoint: server.URL, Model: "text-only", TimeoutSeconds: 2, MaxImageBytes: 1024},
	})
	_, err := reg.Execute("inspect_image", map[string]interface{}{"path": "artifacts/images/generated.png"})
	if err == nil || !strings.Contains(err.Error(), "does not advertise vision") {
		t.Fatalf("expected vision capability rejection, got: %v", err)
	}
}
