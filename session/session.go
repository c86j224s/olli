package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/c86j224s/olli/ollama"
)

type Event struct {
	Timestamp string            `json:"timestamp"`
	Role      string            `json:"role"`
	Content   string            `json:"content,omitempty"`
	Thinking  string            `json:"thinking,omitempty"`
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
	sessionsDir   string
	workspaceRoot string
	currentID     string
	currentFile   *os.File
	lastModel     string
}

var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func NewManager(dir string, allowedRootDir ...string) (*Manager, error) {
	if dir == "" {
		dir = "./sessions"
	}
	rootDir := ""
	if len(allowedRootDir) > 0 {
		rootDir = strings.TrimSpace(allowedRootDir[0])
	}

	sessionsDir, workspaceRoot, err := prepareSessionsDir(dir, rootDir)
	if err != nil {
		return nil, err
	}
	return &Manager{
		sessionsDir:   sessionsDir,
		workspaceRoot: workspaceRoot,
	}, nil
}

func (m *Manager) CreateSession(name string, initialModel string) (*SessionInfo, error) {
	timestamp := time.Now().Format("20060102-150405")
	id := fmt.Sprintf("session_%s", timestamp)
	if name != "" {
		id = strings.TrimSpace(name)
	}

	filePath, err := m.sessionPath(id)
	if err != nil {
		return nil, err
	}
	if err := rejectSessionSymlink(filePath); err != nil {
		return nil, err
	}

	if m.currentFile != nil {
		m.currentFile.Close()
	}

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
	if strings.ContainsAny(query, `/\`) || strings.Contains(query, "..") {
		return "", nil, fmt.Errorf("invalid session search query")
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

	filePath, err := m.sessionPath(resolvedID)
	if err != nil {
		return nil, "", "", err
	}
	if err := rejectSessionSymlink(filePath); err != nil {
		return nil, "", "", err
	}
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

	if err := rejectSessionSymlink(filePath); err != nil {
		return nil, "", "", err
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
	expandPath := func(d string) string {
		d = strings.TrimSpace(d)
		if strings.HasPrefix(d, "~") {
			if home, err := os.UserHomeDir(); err == nil {
				if d == "~" {
					return home
				}
				if strings.HasPrefix(d, "~/") {
					return filepath.Join(home, d[2:])
				}
				return filepath.Join(home, d[1:])
			}
		}
		return d
	}

	for i := len(events) - 1; i >= 0; i-- {
		evt := events[i]
		if evt.Role != "system" {
			continue
		}
		content := evt.Content

		for _, marker := range []string{"📌 [Workspace Directory Updated]:", "📌 [Workspace Directory Initialized]:"} {
			if strings.Contains(content, marker) {
				parts := strings.SplitN(content, marker, 2)
				if len(parts) == 2 {
					dir := expandPath(parts[1])
					if info, err := os.Stat(dir); err == nil && info.IsDir() {
						return dir
					}
				}
			}
		}
	}
	return ""
}

// RenameSession renames an existing session file (or active session if targetID is empty or current)
func (m *Manager) RenameSession(oldQuery string, newName string) (string, error) {
	newName = strings.TrimSpace(newName)
	if err := validateSessionID(newName); err != nil {
		return "", fmt.Errorf("invalid new session name: %w", err)
	}

	targetID := oldQuery
	if targetID == "" {
		targetID = m.currentID
	}

	resolvedID, _, err := m.FindSession(targetID)
	if err != nil {
		return "", err
	}

	oldPath, err := m.sessionPath(resolvedID)
	if err != nil {
		return "", err
	}
	newPath, err := m.sessionPath(newName)
	if err != nil {
		return "", err
	}
	if err := rejectSessionSymlink(oldPath); err != nil {
		return "", err
	}
	if err := rejectSessionSymlink(newPath); err != nil {
		return "", err
	}

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

	filePath, err := m.sessionPath(resolvedID)
	if err != nil {
		return "", err
	}
	if err := rejectSessionSymlink(filePath); err != nil {
		return "", err
	}
	if err := os.Remove(filePath); err != nil {
		return "", fmt.Errorf("failed to delete session file: %w", err)
	}

	return resolvedID, nil
}

// ListSessions returns a slice of SessionInfo sorted by updated_at descending
func (m *Manager) ListSessions() ([]SessionInfo, error) {
	if err := m.ensureSessionsDirSafe(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(m.sessionsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read sessions dir: %w", err)
	}

	var sessions []SessionInfo
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}

		id := strings.TrimSuffix(entry.Name(), ".jsonl")
		if err := validateSessionID(id); err != nil {
			continue
		}
		filePath, err := m.sessionPath(id)
		if err != nil {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}

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

func validateSessionID(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("session ID cannot be empty")
	}
	if id == "." || strings.Contains(id, "..") {
		return fmt.Errorf("session ID cannot contain path traversal")
	}
	if strings.ContainsAny(id, `/\`) {
		return fmt.Errorf("session ID cannot contain path separators")
	}
	if !sessionIDPattern.MatchString(id) {
		return fmt.Errorf("session ID must match [A-Za-z0-9._-]+")
	}
	if filepath.Base(id) != id {
		return fmt.Errorf("session ID must be a basename")
	}
	return nil
}

func (m *Manager) sessionPath(id string) (string, error) {
	if err := m.ensureSessionsDirSafe(); err != nil {
		return "", err
	}
	if err := validateSessionID(id); err != nil {
		return "", err
	}
	filename := id + ".jsonl"
	if filepath.Base(filename) != filename {
		return "", fmt.Errorf("invalid session filename")
	}
	targetPath := filepath.Join(m.sessionsDir, filename)
	rel, err := filepath.Rel(m.sessionsDir, targetPath)
	if err != nil {
		return "", fmt.Errorf("invalid session path: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("session path escapes sessions directory")
	}
	return targetPath, nil
}

func prepareSessionsDir(dir string, rootDir string) (string, string, error) {
	dir = expandSessionPath(strings.TrimSpace(dir))
	if dir == "" {
		return "", "", fmt.Errorf("sessions dir cannot be empty")
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", "", fmt.Errorf("invalid sessions dir: %w", err)
	}
	absDir = filepath.Clean(absDir)

	if info, err := os.Lstat(absDir); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", "", fmt.Errorf("security block: sessions dir '%s' is a symlink and is not allowed", absDir)
	} else if err != nil && !os.IsNotExist(err) {
		return "", "", fmt.Errorf("failed to inspect sessions dir: %w", err)
	}

	root := rootDir
	if root == "" {
		root = absDir
	}
	root = expandSessionPath(root)
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", "", fmt.Errorf("invalid workspace root for sessions dir: %w", err)
	}
	absRoot = filepath.Clean(absRoot)
	if _, err := os.Stat(absRoot); os.IsNotExist(err) && rootDir == "" {
		if err := os.MkdirAll(absRoot, 0755); err != nil {
			return "", "", fmt.Errorf("failed to create sessions dir: %w", err)
		}
	} else if err != nil {
		return "", "", fmt.Errorf("workspace root for sessions dir is not readable: %w", err)
	}

	evalRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", "", fmt.Errorf("workspace root for sessions dir must be resolvable: %w", err)
	}
	rootInfo, err := os.Stat(evalRoot)
	if err != nil {
		return "", "", fmt.Errorf("workspace root for sessions dir cannot be read: %w", err)
	}
	if !rootInfo.IsDir() {
		return "", "", fmt.Errorf("workspace root for sessions dir is not a directory")
	}
	evalRoot = filepath.Clean(evalRoot)

	canonicalDir, err := sessionCanonicalizeNearestExisting(absDir)
	if err != nil {
		return "", "", fmt.Errorf("sessions dir cannot be resolved safely: %w", err)
	}
	if err := sessionEnsureContained(canonicalDir, evalRoot); err != nil {
		return "", "", fmt.Errorf("security block: sessions dir rejected: %w", err)
	}
	if err := os.MkdirAll(canonicalDir, 0755); err != nil {
		return "", "", fmt.Errorf("failed to create sessions dir: %w", err)
	}
	if info, err := os.Lstat(canonicalDir); err != nil {
		return "", "", fmt.Errorf("failed to inspect sessions dir: %w", err)
	} else if info.Mode()&os.ModeSymlink != 0 {
		return "", "", fmt.Errorf("security block: sessions dir '%s' is a symlink and is not allowed", canonicalDir)
	}
	evalDir, err := filepath.EvalSymlinks(canonicalDir)
	if err != nil {
		return "", "", fmt.Errorf("sessions dir must be resolvable: %w", err)
	}
	dirInfo, err := os.Stat(evalDir)
	if err != nil {
		return "", "", fmt.Errorf("sessions dir cannot be read: %w", err)
	}
	if !dirInfo.IsDir() {
		return "", "", fmt.Errorf("sessions dir '%s' is not a directory", evalDir)
	}
	evalDir = filepath.Clean(evalDir)
	if err := sessionEnsureContained(evalDir, evalRoot); err != nil {
		return "", "", fmt.Errorf("security block: sessions dir rejected: %w", err)
	}

	return evalDir, evalRoot, nil
}

func (m *Manager) ensureSessionsDirSafe() error {
	if strings.TrimSpace(m.sessionsDir) == "" {
		return fmt.Errorf("sessions dir is not configured")
	}
	info, err := os.Lstat(m.sessionsDir)
	if err != nil {
		return fmt.Errorf("failed to inspect sessions dir: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("security block: sessions dir '%s' is a symlink and is not allowed", m.sessionsDir)
	}
	evalDir, err := filepath.EvalSymlinks(m.sessionsDir)
	if err != nil {
		return fmt.Errorf("sessions dir must be resolvable: %w", err)
	}
	stat, err := os.Stat(evalDir)
	if err != nil {
		return fmt.Errorf("sessions dir cannot be read: %w", err)
	}
	if !stat.IsDir() {
		return fmt.Errorf("sessions dir '%s' is not a directory", evalDir)
	}
	if m.workspaceRoot != "" {
		if err := sessionEnsureContained(filepath.Clean(evalDir), filepath.Clean(m.workspaceRoot)); err != nil {
			return fmt.Errorf("security block: sessions dir rejected: %w", err)
		}
	}
	return nil
}

func expandSessionPath(path string) string {
	if strings.HasPrefix(path, "~") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			if path == "~" {
				return home
			}
			if strings.HasPrefix(path, "~/") {
				return filepath.Join(home, path[2:])
			}
		}
	}
	return path
}

func sessionCanonicalizeNearestExisting(targetPath string) (string, error) {
	targetPath = filepath.Clean(targetPath)
	if evalPath, err := filepath.EvalSymlinks(targetPath); err == nil {
		return filepath.Clean(evalPath), nil
	} else if !os.IsNotExist(err) {
		return "", err
	}

	parent := targetPath
	var missingParts []string
	for {
		nextParent := filepath.Dir(parent)
		if nextParent == parent {
			return "", fmt.Errorf("target path has no resolvable parent: %s", targetPath)
		}
		missingParts = append([]string{filepath.Base(parent)}, missingParts...)
		parent = nextParent

		evalParent, err := filepath.EvalSymlinks(parent)
		if err == nil {
			info, statErr := os.Stat(evalParent)
			if statErr != nil {
				return "", statErr
			}
			if !info.IsDir() {
				return "", fmt.Errorf("parent '%s' is not a directory", evalParent)
			}
			parts := append([]string{evalParent}, missingParts...)
			return filepath.Clean(filepath.Join(parts...)), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
	}
}

func sessionEnsureContained(target string, root string) error {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	if err != nil {
		return fmt.Errorf("cannot compare path '%s' with root '%s': %w", target, root, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("path '%s' escapes root '%s'", target, root)
	}
	return nil
}

func rejectSessionSymlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to inspect session file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("session file '%s' is a symlink and is not allowed", path)
	}
	return nil
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
