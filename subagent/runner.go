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

const modelHeartbeatInterval = 30 * time.Second

type SubagentRunner struct {
	client            *ollama.Client
	model             string
	cfg               *config.Config
	outputDir         string
	workspace         string
	workspaceRoot     string
	sessionFile       string
	callbacks         SubagentCallbacks
	think             *bool
	budgetOverrides   map[SubagentType]roleBudget
	heartbeatInterval time.Duration
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

func (r *SubagentRunner) withThinking(enabled bool) *SubagentRunner {
	clone := *r
	clone.think = &enabled
	return &clone
}

func (r *SubagentRunner) roleBudget(subType SubagentType) roleBudget {
	if r != nil && r.budgetOverrides != nil {
		if budget, exists := r.budgetOverrides[subType]; exists {
			return budget
		}
	}
	return defaultRoleBudget(subType)
}

func (r *SubagentRunner) heartbeatEvery() time.Duration {
	if r != nil && r.heartbeatInterval > 0 {
		return r.heartbeatInterval
	}
	return modelHeartbeatInterval
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
	if budget := r.roleBudget(SubagentType(subType)); budget.NumPredict > 0 {
		numPredict := budget.NumPredict
		options.NumPredict = &numPredict
	}
	if temperature != nil {
		options.Temperature = *temperature
	}
	definitions := reg.GetDefinitions()
	req := ollama.ChatRequest{
		Model:    r.model,
		Messages: messages,
		Tools:    definitions,
		Options:  options,
		Think:    r.think,
	}
	if len(definitions) == 0 {
		req.Format = format
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
		resp, err := r.callModelWithHeartbeat(ctx, subType, req, streamCB)
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
				resContent := toolRes
				if tErr != nil {
					if strings.TrimSpace(toolRes) == "" {
						resContent = fmt.Sprintf("Error executing tool %s: %v", tc.Function.Name, tErr)
					} else {
						resContent = fmt.Sprintf("%s\nError executing tool %s: %v", toolRes, tc.Function.Name, tErr)
					}
				}
				attempt := evidence.recordAttempt(tc.Function.Name, tc.Function.Arguments, resContent, tErr)
				if tErr == nil {
					successfulToolCalls++
					evidence.recordSuccess(attempt)
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
			if format != nil && evidence != nil && evidence.CompletionReady != nil && evidence.CompletionReady() {
				finalAnswer, err = buildEvidenceCompletion(subType, task, evidence)
				if err == nil {
					logEvent("assistant", finalAnswer, nil)
					termination = agentloop.TerminationSucceeded
					break
				}
				if reason := guard.RecordModelCall(); reason != "" {
					termination = reason
					break
				}
				finalAnswer, err = r.requestStructuredCompletion(ctx, subType, req, messages, streamCB, format)
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
				if reason := guard.RecordModelCall(); reason != "" {
					termination = reason
					break
				}
				finalAnswer, err = r.requestStructuredCompletion(ctx, subType, req, messages, streamCB, format)
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
		if missing := evidence.missingRequiredTools(); len(missing) > 0 {
			guidance := ollama.Message{Role: "system", Content: fmt.Sprintf("The task is not complete. Before returning the final JSON, successfully call these required tools: %s.", strings.Join(missing, ", "))}
			messages = append(messages, *resp, guidance)
			req.Messages = messages
			logEvent("assistant", finalAnswer, nil)
			logEvent("system", guidance.Content, nil)
			finalAnswer = ""
			if reason := guard.ObserveProgress(fmt.Sprintf("required-tools:%d", evidence.ToolCallsSucceeded)); reason != "" {
				termination = reason
				break
			}
			continue
		}
		if format != nil && successfulToolCalls > 0 && looksLikeJSONObject(finalAnswer) {
			termination = agentloop.TerminationSucceeded
			logEvent("assistant", finalAnswer, nil)
			break
		}
		if format != nil && successfulToolCalls > 0 {
			messages = append(messages, *resp)
			if reason := guard.RecordModelCall(); reason != "" {
				termination = reason
				break
			}
			finalAnswer, err = r.requestStructuredCompletion(ctx, subType, req, messages, streamCB, format)
			if err != nil {
				return nil, err
			}
			if !looksLikeJSONObject(finalAnswer) {
				termination = agentloop.TerminationInvalidOutput
				logEvent("assistant", finalAnswer, nil)
				break
			}
		}
		termination = agentloop.TerminationSucceeded
		logEvent("assistant", finalAnswer, nil)
		break
	}

	if termination == agentloop.TerminationSucceeded && format != nil && successfulToolCalls > 0 && !looksLikeJSONObject(finalAnswer) {
		if reason := guard.RecordFormatRepair(); reason == "" {
			if reason := guard.RecordModelCall(); reason == "" {
				finalAnswer, err = r.requestStructuredCompletion(ctx, subType, req, messages, streamCB, format)
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

func (r *SubagentRunner) callModelWithHeartbeat(ctx context.Context, subType string, req ollama.ChatRequest, streamCB ollama.StreamCallbacks) (*ollama.Message, error) {
	if r.callbacks.OnModelHeartbeat == nil {
		return r.client.ChatStreamFullWithContext(ctx, req, streamCB)
	}
	type result struct {
		message *ollama.Message
		err     error
	}
	started := time.Now()
	resultChan := make(chan result, 1)
	go func() {
		message, err := r.client.ChatStreamFullWithContext(ctx, req, streamCB)
		resultChan <- result{message: message, err: err}
	}()
	ticker := time.NewTicker(r.heartbeatEvery())
	defer ticker.Stop()
	for {
		select {
		case completed := <-resultChan:
			return completed.message, completed.err
		case <-ticker.C:
			r.callbacks.OnModelHeartbeat(subType, time.Since(started).Round(time.Second))
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
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

func buildEvidenceCompletion(subType string, task string, evidence *executionEvidence) (string, error) {
	switch SubagentType(subType) {
	case TypeCoder:
		var codeTask CodeTask
		if err := json.Unmarshal([]byte(task), &codeTask); err != nil {
			return "", fmt.Errorf("decode coder task: %w", err)
		}
		report := codeReportFromEvidence(codeTask.Step, evidence)
		report.Unresolved = nil
		report.EvidenceDerived = false
		report.Completed = append([]string(nil), codeTask.Step.Acceptance...)
		if len(report.Completed) == 0 {
			report.Completed = []string{codeTask.Step.Objective}
		}
		changed := make(map[string]struct{}, len(report.ChangedFiles))
		for _, path := range report.ChangedFiles {
			changed[path] = struct{}{}
		}
		for _, finding := range codeTask.ReviewFixes {
			status := "not_addressed"
			evidenceText := "no successful writer tool call changed the finding's file"
			if _, exists := changed[finding.File]; exists {
				status = "addressed"
				evidenceText = "a successful writer tool call changed the finding's planned file; Tester and Reviewer must verify the required outcome"
			}
			report.AddressedFindings = append(report.AddressedFindings, AddressedFinding{
				ID:       finding.ID,
				Status:   status,
				Evidence: evidenceText,
			})
		}
		encoded, err := json.Marshal(report)
		return string(encoded), err
	case TypeTester:
		var payload struct {
			RequiredCommands []string `json:"required_commands"`
		}
		if err := json.Unmarshal([]byte(task), &payload); err != nil {
			return "", fmt.Errorf("decode tester task: %w", err)
		}
		encoded, err := json.Marshal(testReportFromEvidence(payload.RequiredCommands, evidence))
		return string(encoded), err
	default:
		return "", fmt.Errorf("%s requires model-authored structured completion", subType)
	}
}

func (r *SubagentRunner) requestStructuredCompletion(ctx context.Context, subType string, req ollama.ChatRequest, messages []ollama.Message, streamCB ollama.StreamCallbacks, format any) (string, error) {
	if format == nil {
		return "", fmt.Errorf("structured completion format is required")
	}
	var evidence strings.Builder
	for _, message := range messages {
		switch message.Role {
		case "user":
			evidence.WriteString("TASK:\n")
			evidence.WriteString(message.Content)
			evidence.WriteString("\n")
		case "tool":
			evidence.WriteString("TOOL RESULT:\n")
			evidence.WriteString(message.Content)
			evidence.WriteString("\n")
		}
	}
	systemPrompt := "Return only the final JSON object matching the supplied schema. Use the task and tool evidence below. Do not call tools and do not add prose."
	if len(messages) > 0 && messages[0].Role == "system" && strings.TrimSpace(messages[0].Content) != "" {
		systemPrompt += "\n\nROLE CONTRACT:\n" + messages[0].Content
	}
	completionReq := ollama.ChatRequest{
		Model: req.Model,
		Messages: []ollama.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: evidence.String()},
		},
		Format:  format,
		Options: req.Options,
		Think:   req.Think,
	}
	completion, err := r.callModelWithHeartbeat(ctx, subType, completionReq, streamCB)
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
