package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/c86j224s/olli/config"
	agentloop "github.com/c86j224s/olli/loop"
	"github.com/c86j224s/olli/ollama"
	"github.com/c86j224s/olli/session"
	"github.com/c86j224s/olli/tools"
	"golang.org/x/sys/unix"
)

type SubagentRunner struct {
	client        *ollama.Client
	model         string
	cfg           *config.Config
	outputDir     string
	workspace     string
	workspaceRoot string
	sessionFile   string
	callbacks     SubagentCallbacks
}

func NewRunner(client *ollama.Client, model string, cfg *config.Config, workspace string, sessionFile string, callbacks SubagentCallbacks, workspaceRootArg ...string) *SubagentRunner {
	if workspace == "" {
		workspace = "."
	}
	workspaceRoot := workspace
	if len(workspaceRootArg) > 0 && strings.TrimSpace(workspaceRootArg[0]) != "" {
		workspaceRoot = workspaceRootArg[0]
	}

	if safeRoot, err := tools.IsPathSafeFrom(".", workspaceRoot, workspaceRoot); err == nil {
		workspaceRoot = safeRoot
	}
	if safeWorkspace, err := tools.IsPathSafeFrom(".", workspace, workspaceRoot); err == nil {
		workspace = safeWorkspace
	} else {
		workspace = workspaceRoot
	}
	outDir, err := prepareSubagentOutputDir(workspace, workspaceRoot)
	if err != nil {
		outDir = ""
	}

	return &SubagentRunner{
		client:        client,
		model:         model,
		cfg:           cfg,
		outputDir:     outDir,
		workspace:     workspace,
		workspaceRoot: workspaceRoot,
		sessionFile:   sessionFile,
		callbacks:     callbacks,
	}
}

func (r *SubagentRunner) GetSessionFile() string {
	return r.sessionFile
}

func (r *SubagentRunner) GetWorkspaceRoot() string {
	return r.workspaceRoot
}

func (r *SubagentRunner) withModel(model string) *SubagentRunner {
	clone := *r
	if strings.TrimSpace(model) != "" {
		clone.model = strings.TrimSpace(model)
	}
	return &clone
}

func (r *SubagentRunner) newRoleRegistry() *tools.Registry {
	reg := tools.NewEmptyRegistry()
	reg.SetWorkspaceRoot(r.workspaceRoot)
	reg.SetWorkspace(r.workspace)
	reg.SetSessionFile(r.sessionFile)
	return reg
}

func (r *SubagentRunner) executeSubagentLoop(subID string, subType string, task string, sysPrompt string, reg *tools.Registry) (*ResultReport, error) {
	return r.executeSubagentLoopWithContext(context.Background(), subID, subType, task, sysPrompt, reg)
}

func (r *SubagentRunner) executeSubagentLoopWithContext(ctx context.Context, subID string, subType string, task string, sysPrompt string, reg *tools.Registry) (*ResultReport, error) {
	return r.executeSubagentLoopWithFormat(ctx, subID, subType, task, sysPrompt, reg, nil, nil, nil)
}

func (r *SubagentRunner) executeSubagentLoopWithFormat(ctx context.Context, subID string, subType string, task string, sysPrompt string, reg *tools.Registry, format any, temperature *float64, evidence *executionEvidence) (*ResultReport, error) {
	if r.outputDir == "" {
		return nil, fmt.Errorf("subagent output directory is not safely contained within the workspace root")
	}
	jsonlPath := filepath.Join(r.outputDir, subID+".jsonl")
	jsonlFile, err := openSubagentLogFileNoFollow(jsonlPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create subagent jsonl: %w", err)
	}
	defer jsonlFile.Close()
	var logErr error

	logEvent := func(role string, content string, toolCalls []ollama.ToolCall) {
		evt := session.Event{
			Timestamp: time.Now().Format(time.RFC3339),
			Role:      role,
			Content:   content,
			ToolCalls: toolCalls,
		}
		data, _ := json.Marshal(evt)
		if logErr != nil {
			return
		}
		if _, err := jsonlFile.Write(append(data, '\n')); err != nil {
			logErr = err
			return
		}
		if err := jsonlFile.Sync(); err != nil {
			logErr = err
		}
	}

	messages := []ollama.Message{
		{Role: "system", Content: sysPrompt},
		{Role: "user", Content: task},
	}
	logEvent("system", sysPrompt, nil)
	logEvent("user", task, nil)
	if logErr != nil {
		return nil, fmt.Errorf("failed to write subagent jsonl: %w", logErr)
	}

	numCtx := 32768
	if r.cfg != nil && r.cfg.NumCtx > 0 {
		numCtx = r.cfg.NumCtx
	}

	options := &ollama.Options{NumCtx: numCtx}
	if temperature != nil {
		options.Temperature = *temperature
	}
	req := ollama.ChatRequest{
		Model:    r.model,
		Messages: messages,
		Tools:    reg.GetDefinitions(),
		Format:   format,
		Options:  options,
	}

	toolCallsRun := 0
	successfulToolCalls := 0
	var finalAnswer string
	var artifactFiles []string
	var createdFiles []string

	thinkingActive := false
	streamCB := ollama.StreamCallbacks{
		OnThinking: func(token string) {
			if !thinkingActive {
				thinkingActive = true
				if r.callbacks.OnThinkingStart != nil {
					r.callbacks.OnThinkingStart(subType)
				}
			}
			if r.callbacks.OnThinkingToken != nil {
				r.callbacks.OnThinkingToken(token)
			}
		},
		OnContent: func(token string) {
			if thinkingActive {
				thinkingActive = false
				if r.callbacks.OnThinkingEnd != nil {
					r.callbacks.OnThinkingEnd()
				}
			}
		},
	}

	policy := agentloop.DefaultPolicy(format != nil)
	guard, err := agentloop.NewController(policy)
	if err != nil {
		return nil, err
	}
	termination := agentloop.TerminationFailed
	for {
		if reason := guard.BeginIteration(); reason != "" {
			termination = reason
			break
		}
		select {
		case <-ctx.Done():
			reason, status, summary := contextTermination(ctx)
			metrics := guard.Terminate(reason)
			logEvent("system", summary, nil)
			return &ResultReport{
				SubagentID:    subID,
				Type:          subType,
				Task:          task,
				Status:        status,
				Summary:       summary,
				JSONLFile:     jsonlPath,
				ToolCallsRun:  toolCallsRun,
				WorkingDir:    r.workspace,
				Termination:   reason,
				LoopMetrics:   &metrics,
				ArtifactFiles: artifactFiles,
				CreatedFiles:  createdFiles,
			}, nil
		default:
		}

		if reason := guard.RecordModelCall(); reason != "" {
			termination = reason
			break
		}
		resp, err := r.client.ChatStreamFullWithContext(ctx, req, streamCB)
		if err != nil {
			if ctx.Err() != nil || err == context.Canceled || err == context.DeadlineExceeded {
				reason, status, summary := contextTermination(ctx)
				metrics := guard.Terminate(reason)
				logEvent("system", summary, nil)
				return &ResultReport{
					SubagentID:    subID,
					Type:          subType,
					Task:          task,
					Status:        status,
					Summary:       summary,
					JSONLFile:     jsonlPath,
					ToolCallsRun:  toolCallsRun,
					WorkingDir:    r.workspace,
					Termination:   reason,
					LoopMetrics:   &metrics,
					ArtifactFiles: artifactFiles,
					CreatedFiles:  createdFiles,
				}, nil
			}
			return nil, fmt.Errorf("subagent LLM stream failed: %w", err)
		}

		if thinkingActive {
			thinkingActive = false
			if r.callbacks.OnThinkingEnd != nil {
				r.callbacks.OnThinkingEnd()
			}
		}

		if len(resp.ToolCalls) > 0 {
			messages = append(messages, *resp)
			logEvent("assistant", resp.Content, resp.ToolCalls)

			for _, tc := range resp.ToolCalls {
				if ctx.Err() != nil {
					reason, status, summary := contextTermination(ctx)
					metrics := guard.Terminate(reason)
					logEvent("system", summary, nil)
					return &ResultReport{
						SubagentID:    subID,
						Type:          subType,
						Task:          task,
						Status:        status,
						Summary:       summary,
						JSONLFile:     jsonlPath,
						ToolCallsRun:  toolCallsRun,
						WorkingDir:    r.workspace,
						Termination:   reason,
						LoopMetrics:   &metrics,
						ArtifactFiles: artifactFiles,
						CreatedFiles:  createdFiles,
					}, nil
				}

				if reason := guard.RecordToolCall(tc.Function.Name, tc.Function.Arguments); reason != "" {
					termination = reason
					break
				}
				toolCallsRun++
				candidatePath, existedBefore, isArtifactCandidate := artifactCandidatePath(tc.Function.Arguments, r.workspace, r.workspaceRoot)
				toolRes, tErr := reg.ExecuteContext(ctx, tc.Function.Name, tc.Function.Arguments)
				evidence.recordAttempt(tc.Function.Name, tc.Function.Arguments, toolRes, tErr)
				if tErr == nil {
					successfulToolCalls++
					evidence.recordSuccess(tc.Function.Name, tc.Function.Arguments, toolRes)
				}
				resContent := toolRes
				if tErr != nil {
					resContent = fmt.Sprintf("Error executing tool %s: %v", tc.Function.Name, tErr)
				}
				if tErr == nil && isArtifactWriteTool(tc.Function.Name) && isArtifactCandidate {
					req := artifactRequirementForSubagent(subType)
					if err := validateArtifactPath(candidatePath, r.workspaceRoot, req); err == nil {
						artifactFiles = appendUniquePath(artifactFiles, candidatePath)
						if !existedBefore {
							createdFiles = appendUniquePath(createdFiles, candidatePath)
						}
					}
				}

				if r.callbacks.OnToolCall != nil {
					r.callbacks.OnToolCall(subType, tc.Function.Name, tc.Function.Arguments, resContent, tErr)
				}

				toolMsg := ollama.Message{Role: "tool", Content: resContent}
				messages = append(messages, toolMsg)
				logEvent("tool", resContent, nil)
			}

			req.Messages = messages
			if termination != agentloop.TerminationFailed {
				break
			}
			if guard.ConsumeRepetitionRepair() {
				guidance := ollama.Message{Role: "system", Content: "The same tool action was repeated without progress. Do not repeat it. Choose a different valid action or return the final answer now."}
				messages = append(messages, guidance)
				req.Messages = messages
				logEvent("system", guidance.Content, nil)
			}
			progress := fmt.Sprintf("successful-tools:%d", successfulToolCalls)
			if evidence != nil {
				if evidence.ProgressState != nil {
					progress = evidence.ProgressState()
				} else if len(evidence.SuccessfulCalls) > 0 {
					if marker := evidence.SuccessfulCalls[len(evidence.SuccessfulCalls)-1].ProgressMarker; marker != "" {
						progress = marker
					}
				}
			}
			if reason := guard.ObserveProgress(progress); reason != "" {
				termination = reason
				break
			}
			if guard.Metrics().Iterations >= policy.MaxIterations && format != nil {
				if reason := guard.RecordFormatRepair(); reason != "" {
					termination = reason
					break
				}
				if reason := guard.RecordModelCall(); reason != "" {
					termination = reason
					break
				}
				finalAnswer, err = r.requestStructuredCompletion(ctx, req, messages, streamCB)
				if err != nil {
					return nil, err
				}
				logEvent("assistant", finalAnswer, nil)
				if looksLikeJSONObject(finalAnswer) {
					termination = agentloop.TerminationSucceeded
				} else {
					termination = agentloop.TerminationInvalidOutput
				}
				break
			}
			continue
		}

		finalAnswer = resp.Content
		termination = agentloop.TerminationSucceeded
		logEvent("assistant", finalAnswer, nil)
		break
	}

	if termination == agentloop.TerminationSucceeded && format != nil && successfulToolCalls > 0 && !looksLikeJSONObject(finalAnswer) {
		if reason := guard.RecordFormatRepair(); reason == "" {
			if reason := guard.RecordModelCall(); reason == "" {
				finalAnswer, err = r.requestStructuredCompletion(ctx, req, messages, streamCB)
				if err != nil {
					return nil, err
				}
				logEvent("assistant", finalAnswer, nil)
				if looksLikeJSONObject(finalAnswer) {
					termination = agentloop.TerminationSucceeded
				} else {
					termination = agentloop.TerminationInvalidOutput
				}
			} else {
				termination = reason
			}
		} else {
			termination = reason
		}
	}
	if termination != agentloop.TerminationSucceeded {
		metrics := guard.Terminate(termination)
		summary := fmt.Sprintf("subagent loop terminated: %s", termination)
		logEvent("system", summary, nil)
		return &ResultReport{
			SubagentID:    subID,
			Type:          subType,
			Task:          task,
			Status:        "FAILED",
			Summary:       summary,
			JSONLFile:     jsonlPath,
			ToolCallsRun:  toolCallsRun,
			WorkingDir:    r.workspace,
			Termination:   termination,
			LoopMetrics:   &metrics,
			ArtifactFiles: artifactFiles,
			CreatedFiles:  createdFiles,
		}, nil
	}
	if finalAnswer == "" {
		finalAnswer = "Subagent task completed without content."
	}

	artifactReq := artifactRequirementForSubagent(subType)
	artifactFiles = validArtifactFiles(artifactFiles, r.workspaceRoot, artifactReq)
	if artifactReq.required && len(artifactFiles) == 0 {
		summary := fmt.Sprintf("%s subagent did not create or update a required artifact file (%s).", subType, artifactReq.description)
		logEvent("system", "Subagent artifact verification failed: "+summary, nil)
		return &ResultReport{
			SubagentID:    subID,
			Type:          subType,
			Task:          task,
			Status:        "FAILED",
			Summary:       summary,
			JSONLFile:     jsonlPath,
			ToolCallsRun:  toolCallsRun,
			WorkingDir:    r.workspace,
			Termination:   agentloop.TerminationFailed,
			ArtifactFiles: artifactFiles,
			CreatedFiles:  createdFiles,
		}, nil
	}
	if len(artifactFiles) > 0 || len(createdFiles) > 0 {
		logEvent("system", fmt.Sprintf("Subagent artifacts verified. Artifact Files: %s; Created Files: %s", pathListOrNone(artifactFiles), pathListOrNone(createdFiles)), nil)
	}

	metrics := guard.Terminate(agentloop.TerminationSucceeded)
	report := &ResultReport{
		SubagentID:    subID,
		Type:          subType,
		Task:          task,
		Status:        "SUCCESS",
		Summary:       strings.TrimSpace(finalAnswer),
		JSONLFile:     jsonlPath,
		ToolCallsRun:  toolCallsRun,
		WorkingDir:    r.workspace,
		Termination:   agentloop.TerminationSucceeded,
		LoopMetrics:   &metrics,
		ArtifactFiles: artifactFiles,
		CreatedFiles:  createdFiles,
	}

	if logErr != nil {
		return nil, fmt.Errorf("failed to write subagent jsonl: %w", logErr)
	}
	return report, nil
}

func contextTermination(ctx context.Context) (agentloop.TerminationReason, string, string) {
	if ctx != nil && ctx.Err() == context.DeadlineExceeded {
		return agentloop.TerminationTimedOut, "TIMED_OUT", "Subagent execution timed out."
	}
	return agentloop.TerminationCancelled, "INTERRUPTED", "Subagent execution was canceled."
}

func looksLikeJSONObject(value string) bool {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "{") || !strings.HasSuffix(value, "}") {
		return false
	}
	var decoded map[string]any
	decoder := json.NewDecoder(strings.NewReader(value))
	if err := decoder.Decode(&decoded); err != nil || decoded == nil {
		return false
	}
	var trailing any
	return decoder.Decode(&trailing) == io.EOF
}

func (r *SubagentRunner) requestStructuredCompletion(ctx context.Context, req ollama.ChatRequest, messages []ollama.Message, streamCB ollama.StreamCallbacks) (string, error) {
	completionReq := req
	completionReq.Tools = nil
	completionReq.Messages = append(append([]ollama.Message(nil), messages...), ollama.Message{Role: "system", Content: "Tool use is complete. Return only the final JSON object matching the required schema now. Do not call tools."})
	completion, err := r.client.ChatStreamFullWithContext(ctx, completionReq, streamCB)
	if err != nil {
		return "", fmt.Errorf("subagent final structured response failed: %w", err)
	}
	return completion.Content, nil
}

func openSubagentLogFileNoFollow(path string) (*os.File, error) {
	if info, err := os.Lstat(filepath.Dir(path)); err != nil {
		return nil, fmt.Errorf("failed to inspect subagent log directory: %w", err)
	} else if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("subagent log directory '%s' is a symlink and is not allowed", filepath.Dir(path))
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("subagent log file '%s' is a symlink and is not allowed", path)
	} else if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to inspect subagent log file: %w", err)
	}

	fd, err := unix.Open(path, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

func prepareSubagentOutputDir(workspace string, workspaceRoot string) (string, error) {
	outDir, err := tools.IsPathSafeFrom(filepath.Join("sessions", "subagents"), workspace, workspaceRoot)
	if err != nil {
		return "", err
	}
	lexicalPath, lexicalRoot, err := subagentOutputLexicalPath(filepath.Join("sessions", "subagents"), workspace, workspaceRoot)
	if err != nil {
		return "", err
	}
	if err := rejectSubagentOutputSymlinkComponents(lexicalPath, lexicalRoot, true); err != nil {
		return "", err
	}
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return "", err
	}
	if err := rejectSubagentOutputSymlinkComponents(lexicalPath, lexicalRoot, false); err != nil {
		return "", err
	}
	info, err := os.Lstat(outDir)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("subagent output directory '%s' is a symlink and is not allowed", outDir)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("subagent output path '%s' is not a directory", outDir)
	}
	return outDir, nil
}

func subagentOutputLexicalPath(path string, workspace string, workspaceRoot string) (string, string, error) {
	root := strings.TrimSpace(tools.ExpandTilde(workspaceRoot))
	if root == "" {
		return "", "", fmt.Errorf("workspace root cannot be empty")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", "", fmt.Errorf("invalid workspace root: %w", err)
	}
	absRoot = filepath.Clean(absRoot)
	canonicalRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", "", fmt.Errorf("workspace root must be resolvable: %w", err)
	}
	canonicalRoot = filepath.Clean(canonicalRoot)

	safeWorkspace, err := tools.IsPathSafeFrom(".", workspace, canonicalRoot)
	if err != nil {
		return "", "", err
	}
	target := strings.TrimSpace(tools.ExpandTilde(path))
	if !filepath.IsAbs(target) {
		target = filepath.Join(safeWorkspace, target)
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", "", fmt.Errorf("invalid subagent output path: %w", err)
	}
	absTarget = filepath.Clean(absTarget)

	if pathContainedBy(absTarget, absRoot) {
		return absTarget, absRoot, nil
	}
	if pathContainedBy(absTarget, canonicalRoot) {
		return absTarget, canonicalRoot, nil
	}
	return "", "", fmt.Errorf("subagent output path '%s' escapes workspace root '%s'", absTarget, canonicalRoot)
}

func rejectSubagentOutputSymlinkComponents(path string, root string, allowMissing bool) error {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("cannot compare subagent output path '%s' with workspace root '%s': %w", path, root, err)
	}

	current := filepath.Clean(root)
	for _, component := range strings.Split(rel, string(os.PathSeparator)) {
		if component == "" || component == "." {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) && allowMissing {
			return nil
		}
		if err != nil {
			return fmt.Errorf("subagent output path '%s' cannot be inspected: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("subagent output path '%s' is a symlink and is not allowed", current)
		}
	}
	return nil
}
