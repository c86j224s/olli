package runstate

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/sys/unix"
)

var runIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func ValidRunID(value string) bool { return runIDPattern.MatchString(value) }

type Recorder struct {
	root string
}

func NewRecorder(workspaceRoot string) (*Recorder, error) {
	root, err := filepath.Abs(strings.TrimSpace(workspaceRoot))
	if err != nil || root == "" {
		return nil, fmt.Errorf("invalid recorder workspace root")
	}
	dir := filepath.Join(root, "sessions", "runs")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create run event directory: %w", err)
	}
	if err := rejectSymlinkPath(root, dir); err != nil {
		return nil, err
	}
	return &Recorder{root: root}, nil
}

func (r *Recorder) Append(event Event) error {
	if r == nil || !runIDPattern.MatchString(event.RunID) {
		return fmt.Errorf("invalid run id")
	}
	path := filepath.Join(r.root, "sessions", "runs", event.RunID+".jsonl")
	if err := rejectSymlinkPath(r.root, path); err != nil {
		return err
	}
	fd, err := unix.Open(path, unix.O_WRONLY|unix.O_CREAT|unix.O_APPEND|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return fmt.Errorf("open run event log: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return fmt.Errorf("open run event log descriptor")
	}
	defer file.Close()
	encoded, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(encoded, '\n')); err != nil {
		return err
	}
	return file.Sync()
}

func (r *Recorder) Load(runID string, limit int) ([]Event, error) {
	if r == nil || !runIDPattern.MatchString(runID) {
		return nil, fmt.Errorf("invalid run id")
	}
	path := filepath.Join(r.root, "sessions", "runs", runID+".jsonl")
	if err := rejectSymlinkPath(r.root, path); err != nil {
		return nil, err
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("open run event log descriptor")
	}
	defer file.Close()
	var events []Event
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var event Event
		if json.Unmarshal(scanner.Bytes(), &event) == nil {
			events = append(events, event)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if limit > 0 && len(events) > limit {
		events = events[len(events)-limit:]
	}
	return events, nil
}

func (r *Recorder) List() ([]string, error) {
	if r == nil {
		return nil, nil
	}
	dir := filepath.Join(r.root, "sessions", "runs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() || filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".jsonl")
		if runIDPattern.MatchString(id) {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func rejectSymlinkPath(root, target string) error {
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("run event path escapes workspace")
	}
	current := root
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("run event path contains symlink")
		}
	}
	return nil
}
