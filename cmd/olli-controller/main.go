package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/c86j224s/olli/controller"
)

func main() {
	workspaceFlag := flag.String("workspace", "", "workspace root")
	configFlag := flag.String("config", "", "config.json path")
	bindFlag := flag.String("bind", "127.0.0.1:8766", "controller bind address")
	allowRemote := flag.Bool("allow-remote-bind", false, "allow a non-loopback bind (requires bearer token)")
	tokenEnv := flag.String("token-env", "OLLI_CONTROLLER_TOKEN", "bearer token environment variable")
	originsFlag := flag.String("allowed-origins", "", "comma-separated allowed Origin values")
	flag.Parse()
	workspace, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		fatal(err)
	}
	configPath := *configFlag
	if configPath == "" {
		configPath = filepath.Join(workspace, "config.json")
	}
	service, err := controller.New(workspace, configPath, controller.Hooks{OnDiagnostic: func(message string) { _, _ = fmt.Fprintln(os.Stderr, message) }})
	if err != nil {
		fatal(err)
	}
	defer service.Close()
	server, err := controller.NewHTTPServer(service, controller.HTTPOptions{Bind: *bindFlag, BearerToken: os.Getenv(*tokenEnv), AllowRemoteBind: *allowRemote, AllowedOrigins: splitOrigins(*originsFlag)})
	if err != nil {
		fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	fmt.Printf("O.L.L.I. Controller listening on %s\n", server.Addr())
	if err := server.Serve(); err != nil {
		fatal(err)
	}
}

func splitOrigins(value string) []string {
	var result []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}
func resolveWorkspace(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return os.Getwd()
	}
	return filepath.Abs(value)
}
func fatal(err error) { _, _ = fmt.Fprintln(os.Stderr, err); os.Exit(1) }
