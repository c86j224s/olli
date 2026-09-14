package subagent

import (
	"context"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/c86j224s/olli/tools"
)

func runStaticPreflight(ctx context.Context, workspace string, files []string) *TestReport {
	command := CommandResult{Command: "static_preflight", ExitCode: 0, Output: "static source inspection passed"}
	if err := staticPreflight(ctx, workspace, files); err != nil {
		command.ExitCode = 1
		command.Output = err.Error()
		return &TestReport{Passed: false, Commands: []CommandResult{command}, Summary: "static preflight failed"}
	}
	return &TestReport{Passed: true, Commands: []CommandResult{command}, Summary: "static preflight passed"}
}

func staticPreflight(ctx context.Context, workspace string, files []string) error {
	if ctx == nil {
		return fmt.Errorf("static preflight context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var goFiles []string
	for _, path := range uniqueStrings(files) {
		normalized, err := normalizePlanPath(path)
		if err != nil {
			return fmt.Errorf("static preflight file %q: %w", path, err)
		}
		if strings.EqualFold(filepath.Ext(normalized), ".go") {
			goFiles = append(goFiles, normalized)
		}
	}
	if len(goFiles) == 0 {
		return nil
	}
	dirs := make(map[string]struct{})
	for _, relative := range goFiles {
		absolute, err := tools.IsPathSafeFrom(relative, workspace, workspace)
		if err != nil {
			return fmt.Errorf("static preflight file %q: %w", relative, err)
		}
		dirs[filepath.Dir(absolute)] = struct{}{}
	}
	orderedDirs := make([]string, 0, len(dirs))
	for dir := range dirs {
		orderedDirs = append(orderedDirs, dir)
	}
	sort.Strings(orderedDirs)
	for _, dir := range orderedDirs {
		if err := staticPreflightDirectory(ctx, workspace, dir); err != nil {
			return err
		}
	}
	return nil
}

func staticPreflightDirectory(ctx context.Context, workspace string, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", filepath.Base(dir), err)
	}
	set := token.NewFileSet()
	packages := make(map[string][]*ast.File)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".go") || strings.HasSuffix(strings.ToLower(entry.Name()), "_test.go") {
			continue
		}
		absolute := filepath.Join(dir, entry.Name())
		if _, err := tools.IsPathSafeFrom(absolute, workspace, workspace); err != nil {
			return fmt.Errorf("static preflight file %q: %w", entry.Name(), err)
		}
		info, err := os.Lstat(absolute)
		if err != nil {
			return fmt.Errorf("inspect %s: %w", entry.Name(), err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular source file", entry.Name())
		}
		file, err := parser.ParseFile(set, absolute, nil, parser.AllErrors)
		if err != nil {
			return fmt.Errorf("parse %s: %w", entry.Name(), err)
		}
		packages[file.Name.Name] = append(packages[file.Name.Name], file)
	}
	packageNames := make([]string, 0, len(packages))
	for name := range packages {
		packageNames = append(packageNames, name)
	}
	sort.Strings(packageNames)
	for _, name := range packageNames {
		config := types.Config{Importer: importer.Default()}
		if _, err := config.Check(name, set, packages[name], nil); err != nil {
			return fmt.Errorf("type check %s: %w", filepath.Base(dir), err)
		}
	}
	return nil
}
