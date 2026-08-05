package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/c86j224s/olli/ollama"
)

type Event struct {
	Timestamp string           `json:"timestamp"`
	Role      string           `json:"role"`
	Content   string           `json:"content,omitempty"`
	Thinking  string           `json:"thinking,omitempty"`
	ToolCalls []ollama.ToolCall `json:"tool_calls,omitempty"`
}

type SessionInfo struct {
	ID           string    `json:"id"`
	FilePath     string    `json:"file_path"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	MessageCount int       `json:"message_count"`
	LastModel    string    `json:"last_model"`
}

type Manager struct {
	sessionsDir string
	currentID   string
	currentFile *os.File
	lastModel   string
}

func NewManager(dir string) (*Manager, error) {
	if dir == "" {
		dir = "./sessions"
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create sessions dir: %w", err)
	}
	return &Manager{
		sessionsDir: dir,
	}, nil
}

func (m *Manager) CreateSession(name string, initialModel string) (*SessionInfo, error) {
	if m.currentFile != nil {
		m.currentFile.Close()
	}

	timestamp := time.Now().Format("20060102-150405")
	id := fmt.Sprintf("session_%s", timestamp)
	if name != "" {
		sanitized := strings.ReplaceAll(name, " ", "_")
		id = sanitized
	}

	filePath := filepath.Join(m.sessionsDir, id+".jsonl")
	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to create session file: %w", err)
	}

	m.currentID = id
	m.currentFile = file
	m.lastModel = initialModel

	info := &SessionInfo{
		ID:           id,
		FilePath:     filePath,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
		MessageCount: 0,
		LastModel:    initialModel,
	}

	return info, nil
}

func (m *Manager) GetCurrentID() string {
	return m.currentID
}

func (m *Manager) GetCurrentPath() string {
	if m.currentFile != nil {
		return m.currentFile.Name()
	}
	return ""
}

func (m *Manager) AppendEvent(msg ollama.Message) error {
	if m.currentFile == nil {
		return fmt.Errorf("no active session")
	}

	evt := Event{
		Timestamp: time.Now().Format(time.RFC3339),
		Role:      msg.Role,
		Content:   msg.Content,
		Thinking:  msg.Thinking,
		ToolCalls: msg.ToolCalls,
	}

	data, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	if _, err := m.currentFile.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("failed to write event to jsonl: %w", err)
	}

	return m.currentFile.Sync()
}

// FindSession resolves exact ID or substring name match to a session ID
func (m *Manager) FindSession(query string) (string, []string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", nil, fmt.Errorf("empty session search query")
	}

	sessions, err := m.ListSessions()
	if err != nil {
		return "", nil, err
	}

	// 1. Exact match by ID
	for _, s := range sessions {
		if s.ID == query || s.ID == query+".jsonl" {
			return s.ID, nil, nil
		}
	}

	// 2. Substring or case-insensitive match
	queryLower := strings.ToLower(query)
	var matches []string

	for _, s := range sessions {
		if strings.Contains(strings.ToLower(s.ID), queryLower) {
			matches = append(matches, s.ID)
		}
	}

	if len(matches) == 1 {
		return matches[0], nil, nil
	} else if len(matches) > 1 {
		return "", matches, fmt.Errorf("multiple session matches found for '%s'", query)
	}

	return "", nil, fmt.Errorf("no session found matching '%s'", query)
}

// LoadSession resolves query (ID or name/alias) and loads messages and last working directory into memory
func (m *Manager) LoadSession(nameOrID string) ([]ollama.Message, string, string, error) {
	resolvedID, matches, err := m.FindSession(nameOrID)
	if err != nil {
		if len(matches) > 0 {
			return nil, "", "", fmt.Errorf("multiple session matches found for '%s': %s", nameOrID, strings.Join(matches, ", "))
		}
		return nil, "", "", err
	}

	filePath := filepath.Join(m.sessionsDir, resolvedID+".jsonl")
	file, err := os.Open(filePath)
	if err != nil {
		return nil, "", "", fmt.Errorf("session file '%s' not found: %w", resolvedID, err)
	}
	defer file.Close()

	if m.currentFile != nil {
		m.currentFile.Close()
	}

	var messages []ollama.Message
	var events []Event
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var evt Event
		if err := json.Unmarshal(line, &evt); err != nil {
			continue
		}

		events = append(events, evt)
		messages = append(messages, ollama.Message{
			Role:      evt.Role,
			Content:   evt.Content,
			Thinking:  evt.Thinking,
			ToolCalls: evt.ToolCalls,
		})
	}

	if err := scanner.Err(); err != nil {
		return nil, "", "", fmt.Errorf("error reading session file: %w", err)
	}

	appendFile, err := os.OpenFile(filePath, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, "", "", fmt.Errorf("failed to open session for append: %w", err)
	}

	m.currentID = resolvedID
	m.currentFile = appendFile

	lastDir := ExtractLastWorkingDir(events)

	return messages, resolvedID, lastDir, nil
}

// ExtractLastWorkingDir scans session events backwards to find the last valid working directory
func ExtractLastWorkingDir(events []Event) string {
	for i := len(events) - 1; i >= 0; i-- {
		evt := events[i]
		content := evt.Content

		// 1. Explicit system marker: 📌 [Workspace Directory Updated]: /path/to/dir
		if strings.Contains(content, "📌 [Workspace Directory Updated]:") {
			parts := strings.SplitN(content, "📌 [Workspace Directory Updated]:", 2)
			if len(parts) == 2 {
				dir := strings.TrimSpace(parts[1])
				if info, err := os.Stat(dir); err == nil && info.IsDir() {
					return dir
				}
			}
		}

		// 2. CD tool output marker: Working directory successfully changed to '/path/to/dir'
		if strings.Contains(content, "Working directory successfully changed to '") {
			start := strings.Index(content, "Working directory successfully changed to '") + len("Working directory successfully changed to '")
			end := strings.Index(content[start:], "'")
			if end != -1 {
				dir := content[start : start+end]
				if info, err := os.Stat(dir); err == nil && info.IsDir() {
					return dir
				}
			}
		}

		// 3. Tool calls with CD arguments
		for _, tc := range evt.ToolCalls {
			if tc.Function.Name == "cd" || tc.Function.Name == "change_directory" {
				if p, ok := tc.Function.Arguments["path"].(string); ok && p != "" {
					trimmed := strings.TrimSpace(p)
					if info, err := os.Stat(trimmed); err == nil && info.IsDir() {
						return trimmed
					}
				}
			}
		}
	}
	return ""
}

// RenameSession renames an existing session file (or active session if targetID is empty or current)
func (m *Manager) RenameSession(oldQuery string, newName string) (string, error) {
	newName = strings.TrimSpace(strings.ReplaceAll(newName, " ", "_"))
	if newName == "" {
		return "", fmt.Errorf("new session name cannot be empty")
	}

	targetID := oldQuery
	if targetID == "" {
		targetID = m.currentID
	}

	resolvedID, _, err := m.FindSession(targetID)
	if err != nil {
		return "", err
	}

	oldPath := filepath.Join(m.sessionsDir, resolvedID+".jsonl")
	newPath := filepath.Join(m.sessionsDir, newName+".jsonl")

	if _, err := os.Stat(newPath); err == nil && resolvedID != newName {
		return "", fmt.Errorf("session with name '%s' already exists", newName)
	}

	isActive := (resolvedID == m.currentID)
	if isActive && m.currentFile != nil {
		m.currentFile.Close()
		m.currentFile = nil
	}

	if err := os.Rename(oldPath, newPath); err != nil {
		return "", fmt.Errorf("failed to rename session file: %w", err)
	}

	if isActive {
		m.currentID = newName
		appendFile, err := os.OpenFile(newPath, os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return "", fmt.Errorf("failed to reopen renamed session: %w", err)
		}
		m.currentFile = appendFile
	}

	return newName, nil
}

// DeleteSession deletes a session by ID or query name
func (m *Manager) DeleteSession(query string) (string, error) {
	resolvedID, _, err := m.FindSession(query)
	if err != nil {
		return "", err
	}

	if resolvedID == m.currentID {
		return "", fmt.Errorf("cannot delete currently active session '%s'. Switch to another session first", resolvedID)
	}

	filePath := filepath.Join(m.sessionsDir, resolvedID+".jsonl")
	if err := os.Remove(filePath); err != nil {
		return "", fmt.Errorf("failed to delete session file: %w", err)
	}

	return resolvedID, nil
}

// ListSessions returns a slice of SessionInfo sorted by updated_at descending
func (m *Manager) ListSessions() ([]SessionInfo, error) {
	entries, err := os.ReadDir(m.sessionsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read sessions dir: %w", err)
	}

	var sessions []SessionInfo
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}

		filePath := filepath.Join(m.sessionsDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}

		id := strings.TrimSuffix(entry.Name(), ".jsonl")
		msgCount, lastModel := m.countMessagesAndModel(filePath)

		sessions = append(sessions, SessionInfo{
			ID:           id,
			FilePath:     filePath,
			CreatedAt:    info.ModTime(),
			UpdatedAt:    info.ModTime(),
			MessageCount: msgCount,
			LastModel:    lastModel,
		})
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})

	return sessions, nil
}

func (m *Manager) countMessagesAndModel(path string) (int, string) {
	file, err := os.Open(path)
	if err != nil {
		return 0, ""
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	count := 0
	lastModel := ""

	for scanner.Scan() {
		count++
		line := scanner.Bytes()
		var evt Event
		if err := json.Unmarshal(line, &evt); err == nil {
			if evt.Role == "assistant" && evt.Content != "" {
				lastModel = "active"
			}
		}
	}

	return count, lastModel
}
