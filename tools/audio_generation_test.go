package tools

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestAudioGenerateRejectsUnsafeEndpointsTempOnlyNoNetwork(t *testing.T) {
	// Blast radius if the guard regresses: temp/repo-local config validation only; no HTTP server is contacted.
	root := testSafeTempRoot(t)
	unsafeEndpoints := []string{
		"https://127.0.0.1:8001",
		"http://example.com:8001",
		"http://user@127.0.0.1:8001",
		"http://127.0.0.1:8001/path",
		"http://127.0.0.1:8001?x=1",
		"http://127.0.0.1:8001#fragment",
	}

	for _, endpoint := range unsafeEndpoints {
		reg := NewRegistry()
		reg.SetWorkspaceRoot(root)
		reg.SetWorkspace(root)
		reg.SetAudioGenerationConfig(AudioGenerationConfig{
			ACEStep: ACEStepConfig{
				Endpoint: endpoint,
			},
		})

		_, err := reg.ExecuteContext(mediaTestContext(reg), "audio_generate", map[string]interface{}{
			"backend_alias": "ace_step",
			"prompt":        "test prompt",
		})
		if err == nil || !strings.Contains(err.Error(), "endpoint") {
			t.Fatalf("expected endpoint rejection for %q, got: %v", endpoint, err)
		}
	}
}

func TestAudioGenerateRejectsRedirectsTempOnlyHttptest(t *testing.T) {
	// Blast radius if the guard regresses: temp workspace artifacts only; redirect target is another httptest server.
	root := testSafeTempRoot(t)

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
	reg.SetAudioGenerationConfig(AudioGenerationConfig{
		ACEStep: ACEStepConfig{
			Endpoint:       server.URL,
			OutputDir:      "artifacts/audio",
			TimeoutSeconds: 2,
			MaxAudioBytes:  1024,
			PollIntervalMS: 100,
		},
	})

	_, err := reg.ExecuteContext(mediaTestContext(reg, server.Config.Handler), "audio_generate", map[string]interface{}{
		"backend_alias": "ace_step",
		"prompt":        "test prompt",
	})
	if err == nil || !strings.Contains(err.Error(), "status 307") {
		t.Fatalf("expected redirect status rejection, got: %v", err)
	}
	if redirectedHits != 0 {
		t.Fatalf("redirect target was reached %d times", redirectedHits)
	}
}

func TestAudioGenerateACEStepSuccessPathTempOnlyHttptest(t *testing.T) {
	// Blast radius if the guard regresses: temp workspace artifacts only; all ACE-Step calls use httptest.
	root := testSafeTempRoot(t)
	wavBytes := testWAVBytes()

	var sawRelease bool
	var sawQuery bool
	var sawAudio bool
	var queryCount int
	var serverMu sync.Mutex
	var serverErrors []string
	recordServerError := func(w http.ResponseWriter, format string, args ...interface{}) {
		msg := formatServerError(format, args...)
		serverMu.Lock()
		serverErrors = append(serverErrors, msg)
		serverMu.Unlock()
		http.Error(w, msg, http.StatusInternalServerError)
	}

	server := newInProcessTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/release_task":
			if r.Method != http.MethodPost {
				recordServerError(w, "expected POST /release_task, got %s", r.Method)
				return
			}
			var req map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				recordServerError(w, "failed to decode release request: %v", err)
				return
			}
			if req["prompt"] != "slow synth pop" {
				recordServerError(w, "prompt was not sent: %#v", req["prompt"])
				return
			}
			if req["lyrics"] != "hello" {
				recordServerError(w, "lyrics were not sent: %#v", req["lyrics"])
				return
			}
			if req["audio_format"] != "wav" || req["task_type"] != "text2music" {
				recordServerError(w, "safe ACE-Step defaults missing: %#v", req)
				return
			}
			if req["batch_size"] != float64(1) {
				recordServerError(w, "batch size was not forced to 1: %#v", req["batch_size"])
				return
			}
			if req["audio_duration"] != float64(12) {
				recordServerError(w, "duration was not mapped: %#v", req["audio_duration"])
				return
			}
			if req["use_random_seed"] != false || req["seed"] != float64(1234) {
				recordServerError(w, "seed controls were not mapped: %#v", req)
				return
			}
			if req["model"] != "acestep-v15-turbo" || req["thinking"] != true || req["vocal_language"] != "ko" {
				recordServerError(w, "optional ACE-Step fields were not mapped: %#v", req)
				return
			}
			sawRelease = true
			w.Header().Set("Content-Type", "application/json")
			writeJSON(t, w, map[string]interface{}{
				"code": 200,
				"data": map[string]interface{}{
					"task_id":        "task-123",
					"status":         "queued",
					"queue_position": 1,
				},
			})
		case "/query_result":
			if r.Method != http.MethodPost {
				recordServerError(w, "expected POST /query_result, got %s", r.Method)
				return
			}
			var req struct {
				TaskIDList []string `json:"task_id_list"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				recordServerError(w, "failed to decode query request: %v", err)
				return
			}
			if len(req.TaskIDList) != 1 || req.TaskIDList[0] != "task-123" {
				recordServerError(w, "unexpected query task ids: %#v", req.TaskIDList)
				return
			}
			sawQuery = true
			queryCount++
			w.Header().Set("Content-Type", "application/json")
			if queryCount == 1 {
				writeJSON(t, w, map[string]interface{}{
					"code": 200,
					"data": []map[string]interface{}{{"task_id": "task-123", "status": 0, "result": "[]"}},
				})
				return
			}
			resultBytes, _ := json.Marshal([]map[string]interface{}{
				{"file": "/v1/audio?path=%2Ftmp%2Fapi_audio%2Funsafe.go", "status": 1},
			})
			writeJSON(t, w, map[string]interface{}{
				"code": 200,
				"data": []map[string]interface{}{{"task_id": "task-123", "status": 1, "result": string(resultBytes)}},
			})
		case "/v1/audio":
			if r.URL.Query().Get("path") != "/tmp/api_audio/unsafe.go" {
				recordServerError(w, "audio path query changed: %q", r.URL.Query().Get("path"))
				return
			}
			sawAudio = true
			w.Header().Set("Content-Type", "audio/wav")
			_, _ = w.Write(wavBytes)
		default:
			recordServerError(w, "unexpected ACE-Step path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	reg := NewRegistry()
	reg.SetWorkspaceRoot(root)
	reg.SetWorkspace(root)
	reg.SetAudioGenerationConfig(AudioGenerationConfig{
		ACEStep: ACEStepConfig{
			Endpoint:           server.URL,
			OutputDir:          "artifacts/audio",
			TimeoutSeconds:     2,
			MaxAudioBytes:      1024,
			PollIntervalMS:     100,
			MaxDurationSeconds: 600,
		},
	})

	resultJSON, err := reg.ExecuteContext(mediaTestContext(reg, server.Config.Handler), "audio_generate", map[string]interface{}{
		"backend_alias":    "ace_step",
		"prompt":           "slow synth pop",
		"lyrics":           "hello",
		"duration_seconds": 12,
		"seed":             1234,
		"model":            "acestep-v15-turbo",
		"thinking":         true,
		"vocal_language":   "ko",
	})
	if err != nil {
		serverMu.Lock()
		defer serverMu.Unlock()
		if len(serverErrors) > 0 {
			t.Fatalf("audio_generate failed after server assertion: %v; server errors: %s", err, strings.Join(serverErrors, "; "))
		}
		t.Fatalf("audio_generate failed: %v", err)
	}
	serverMu.Lock()
	if len(serverErrors) > 0 {
		t.Fatalf("server errors: %s", strings.Join(serverErrors, "; "))
	}
	serverMu.Unlock()
	if !sawRelease || !sawQuery || !sawAudio {
		t.Fatalf("expected release/query/audio calls, got release=%v query=%v audio=%v", sawRelease, sawQuery, sawAudio)
	}

	var result audioGenerateResult
	if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
		t.Fatalf("failed to decode result JSON: %v\n%s", err, resultJSON)
	}
	if err := ensureContained(result.ArtifactPath, root); err != nil {
		t.Fatalf("artifact escaped workspace root: %v", err)
	}
	if !strings.HasPrefix(result.WorkspaceRelativePath, filepath.Join("artifacts", "audio")+string(os.PathSeparator)) {
		t.Fatalf("unexpected relative artifact path: %s", result.WorkspaceRelativePath)
	}
	if !strings.Contains(filepath.Base(result.ArtifactPath), "unsafe.wav") {
		t.Fatalf("returned filename was not sanitized into local WAV artifact name: %s", result.ArtifactPath)
	}
	if strings.HasSuffix(result.ArtifactPath, ".go") {
		t.Fatalf("remote executable extension was preserved: %s", result.ArtifactPath)
	}
	written, err := os.ReadFile(result.ArtifactPath)
	if err != nil {
		t.Fatalf("failed to read artifact: %v", err)
	}
	if _, err := canonicalizeWAVAudio(written, "audio/wav"); err != nil {
		t.Fatalf("artifact is not a valid WAV file: %v", err)
	}
}

func TestCanonicalizeWAVAudioStripsTrailingPayloadTempOnly(t *testing.T) {
	// Blast radius if the guard regresses: in-memory WAV validation only; no filesystem or HTTP mutation.
	payload := []byte("package payload\n")
	wavWithPayload := append(testWAVBytes(), payload...)

	canonical, err := canonicalizeWAVAudio(wavWithPayload, "audio/wav")
	if err != nil {
		t.Fatalf("canonicalizeWAVAudio failed: %v", err)
	}
	if bytes.Contains(canonical, payload) {
		t.Fatal("canonical WAV retained trailing payload")
	}
	if bytes.Equal(canonical, wavWithPayload) {
		t.Fatal("canonical WAV still equals payload-bearing input")
	}
	if _, err := canonicalizeWAVAudio(canonical, "audio/wav"); err != nil {
		t.Fatalf("canonical artifact is not a valid WAV file: %v", err)
	}
}

func TestAudioGenerateRejectsInvalidAudioBytesTempOnlyHttptest(t *testing.T) {
	// Blast radius if the guard regresses: temp workspace artifacts only; all ACE-Step calls use httptest.
	root := testSafeTempRoot(t)
	server := newInProcessTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/release_task":
			w.Header().Set("Content-Type", "application/json")
			writeJSON(t, w, map[string]interface{}{"code": 200, "data": map[string]interface{}{"task_id": "task-123"}})
		case "/query_result":
			resultBytes, _ := json.Marshal([]map[string]interface{}{{"file": "/v1/audio?path=%2Ftmp%2Fpayload.go", "status": 1}})
			w.Header().Set("Content-Type", "application/json")
			writeJSON(t, w, map[string]interface{}{
				"code": 200,
				"data": []map[string]interface{}{{"task_id": "task-123", "status": 1, "result": string(resultBytes)}},
			})
		case "/v1/audio":
			w.Header().Set("Content-Type", "audio/wav")
			_, _ = w.Write([]byte("package payload\n"))
		default:
			http.Error(w, "unexpected path", http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	reg := NewRegistry()
	reg.SetWorkspaceRoot(root)
	reg.SetWorkspace(root)
	reg.SetAudioGenerationConfig(AudioGenerationConfig{
		ACEStep: ACEStepConfig{
			Endpoint:           server.URL,
			OutputDir:          "artifacts/audio",
			TimeoutSeconds:     2,
			MaxAudioBytes:      1024,
			PollIntervalMS:     100,
			MaxDurationSeconds: 600,
		},
	})

	_, err := reg.ExecuteContext(mediaTestContext(reg, server.Config.Handler), "audio_generate", map[string]interface{}{
		"backend_alias": "ace_step",
		"prompt":        "test prompt",
	})
	if err == nil || !strings.Contains(err.Error(), "RIFF/WAVE") {
		t.Fatalf("expected invalid audio rejection, got: %v", err)
	}
	entries, readErr := os.ReadDir(filepath.Join(root, "artifacts", "audio"))
	if readErr != nil {
		t.Fatalf("failed to inspect artifact dir: %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no artifact files after invalid audio, got %d", len(entries))
	}
}

func TestAudioGenerateRejectsSymlinkOutputComponentTempOnlyNoHTTP(t *testing.T) {
	// Blast radius if the guard regresses: temp workspace and temp outside marker only; HTTP must not be reached.
	root := testSafeTempRoot(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "artifacts")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
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
	reg.SetAudioGenerationConfig(AudioGenerationConfig{
		ACEStep: ACEStepConfig{
			Endpoint:       server.URL,
			OutputDir:      "artifacts/audio",
			TimeoutSeconds: 2,
			MaxAudioBytes:  1024,
			PollIntervalMS: 100,
		},
	})

	_, err := reg.ExecuteContext(mediaTestContext(reg), "audio_generate", map[string]interface{}{
		"backend_alias": "ace_step",
		"prompt":        "test prompt",
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

func TestAudioGenerateRejectsOutputDirsOutsideArtifactBaseTempOnly(t *testing.T) {
	// Blast radius if the guard regresses: temp/repo-local output_dir validation only.
	root := testSafeTempRoot(t)
	outside := t.TempDir()
	rejected := []string{
		".",
		"agent",
		"artifacts",
		filepath.Join("artifacts", "images"),
		filepath.Join("artifacts", "..", "agent"),
		filepath.Join(root, "agent"),
		filepath.Join(outside, "audio"),
	}

	for _, outputDir := range rejected {
		if _, err := ensureAudioArtifactOutputDir(outputDir, root); err == nil {
			t.Fatalf("expected output_dir %q to be rejected", outputDir)
		}
	}
	if _, err := ensureAudioArtifactOutputDir(filepath.Join("artifacts", "audio"), root); err != nil {
		t.Fatalf("expected default artifact output dir to pass: %v", err)
	}
	if _, err := ensureAudioArtifactOutputDir(filepath.Join("artifacts", "audio", "run1"), root); err != nil {
		t.Fatalf("expected artifact subdir output dir to pass: %v", err)
	}
}

func TestAudioGenerateRejectsUnsafeResourceBoundsTempOnlyNoNetwork(t *testing.T) {
	// Blast radius if the guard regresses: temp/repo-local argument validation only; no HTTP server is contacted.
	root := testSafeTempRoot(t)
	cases := []map[string]interface{}{
		{"duration_seconds": defaultACEStepMaxDurationSeconds + 1},
		{"seed": -1},
		{"thinking": "true"},
	}

	for _, extraArgs := range cases {
		reg := NewRegistry()
		reg.SetWorkspaceRoot(root)
		reg.SetWorkspace(root)
		reg.SetAudioGenerationConfig(DefaultAudioGenerationConfig())
		args := map[string]interface{}{
			"backend_alias": "ace_step",
			"prompt":        "test prompt",
		}
		for key, value := range extraArgs {
			args[key] = value
		}
		if _, err := reg.ExecuteContext(mediaTestContext(reg), "audio_generate", args); err == nil {
			t.Fatalf("expected resource bounds rejection for args %#v", extraArgs)
		}
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, value interface{}) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("failed to write JSON fixture: %v", err)
	}
}

func formatServerError(format string, args ...interface{}) string {
	return strings.TrimSpace(strings.NewReplacer("\n", " ").Replace(fmt.Sprintf(format, args...)))
}

func testWAVBytes() []byte {
	var buf bytes.Buffer
	buf.WriteString("RIFF")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(0))
	buf.WriteString("WAVE")

	var fmtChunk bytes.Buffer
	_ = binary.Write(&fmtChunk, binary.LittleEndian, uint16(1))
	_ = binary.Write(&fmtChunk, binary.LittleEndian, uint16(1))
	_ = binary.Write(&fmtChunk, binary.LittleEndian, uint32(44100))
	_ = binary.Write(&fmtChunk, binary.LittleEndian, uint32(88200))
	_ = binary.Write(&fmtChunk, binary.LittleEndian, uint16(2))
	_ = binary.Write(&fmtChunk, binary.LittleEndian, uint16(16))
	writeWAVChunk(&buf, "fmt ", fmtChunk.Bytes())

	var dataChunk bytes.Buffer
	_ = binary.Write(&dataChunk, binary.LittleEndian, int16(0))
	writeWAVChunk(&buf, "data", dataChunk.Bytes())

	out := buf.Bytes()
	binary.LittleEndian.PutUint32(out[4:8], uint32(len(out)-8))
	return out
}
