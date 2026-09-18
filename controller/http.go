package controller

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

type HTTPOptions struct {
	Bind            string
	BearerToken     string
	AllowRemoteBind bool
	AllowedOrigins  []string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
}

type HTTPServer struct {
	service  *Service
	server   *http.Server
	listener net.Listener
	token    string
	origins  map[string]struct{}
}

type startRequest struct {
	Prompt string `json:"prompt"`
	RunID  string `json:"run_id,omitempty"`
}
type permissionDecisionRequest struct {
	Allowed bool `json:"allowed"`
	Always  bool `json:"always"`
}
type drainRequest struct {
	Draining bool `json:"draining"`
}

func validateHTTPOptions(options HTTPOptions) (string, string, error) {
	bind := strings.TrimSpace(options.Bind)
	if bind == "" {
		bind = "127.0.0.1:8766"
	}
	host, _, err := net.SplitHostPort(bind)
	if err != nil {
		return "", "", fmt.Errorf("invalid controller bind: %w", err)
	}
	token := strings.TrimSpace(options.BearerToken)
	if host == "" || net.ParseIP(host) != nil && net.ParseIP(host).IsUnspecified() {
		if !options.AllowRemoteBind {
			return "", "", fmt.Errorf("wildcard controller bind requires allow_remote_bind")
		}
	} else if !isLoopbackHost(host) && !options.AllowRemoteBind {
		return "", "", fmt.Errorf("remote controller bind requires allow_remote_bind")
	}
	if !isLoopbackHost(host) && token == "" {
		return "", "", fmt.Errorf("remote controller bind requires bearer token")
	}
	if token != "" && len(token) < 24 {
		return "", "", fmt.Errorf("controller bearer token must be at least 24 characters")
	}
	return bind, token, nil
}

func NewHTTPServer(service *Service, options HTTPOptions) (*HTTPServer, error) {
	if service == nil {
		return nil, fmt.Errorf("controller service is required")
	}
	bind, token, err := validateHTTPOptions(options)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", bind)
	if err != nil {
		return nil, err
	}
	origins := make(map[string]struct{}, len(options.AllowedOrigins))
	for _, origin := range options.AllowedOrigins {
		if origin = strings.TrimSpace(origin); origin != "" {
			origins[origin] = struct{}{}
		}
	}
	h := &HTTPServer{service: service, listener: listener, token: token, origins: origins}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/info", h.handleInfo)
	mux.HandleFunc("GET /api/v1/runs", h.handleRuns)
	mux.HandleFunc("POST /api/v1/runs", h.handleStartRun)
	mux.HandleFunc("POST /api/v1/runs/cancel", h.handleCancel)
	mux.HandleFunc("GET /api/v1/runs/{runID}/events", h.handleEvents)
	mux.HandleFunc("GET /api/v1/permissions", h.handlePermissions)
	mux.HandleFunc("POST /api/v1/permissions/{permissionID}", h.handlePermission)
	mux.HandleFunc("GET /api/v1/gateway/nodes", h.handleGateway)
	mux.HandleFunc("POST /api/v1/gateway/nodes/{nodeID}/drain", h.handleDrain)
	readTimeout := options.ReadTimeout
	if readTimeout <= 0 {
		readTimeout = 15 * time.Second
	}
	writeTimeout := options.WriteTimeout
	if writeTimeout <= 0 {
		writeTimeout = 30 * time.Second
	}
	h.server = &http.Server{Handler: h.middleware(mux), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: readTimeout, WriteTimeout: writeTimeout, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	return h, nil
}

func (h *HTTPServer) Addr() string {
	if h == nil || h.listener == nil {
		return ""
	}
	return h.listener.Addr().String()
}
func (h *HTTPServer) Serve() error {
	err := h.server.Serve(h.listener)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}
func (h *HTTPServer) Shutdown(ctx context.Context) error { return h.server.Shutdown(ctx) }

func (h *HTTPServer) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		if origin := r.Header.Get("Origin"); origin != "" {
			if _, ok := h.origins[origin]; !ok {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		if h.token != "" {
			provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if subtle.ConstantTimeCompare([]byte(provided), []byte(h.token)) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (h *HTTPServer) handleInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.service.ReadyInfo())
}
func (h *HTTPServer) handleRuns(w http.ResponseWriter, _ *http.Request) {
	runs, err := h.service.ListRuns()
	writeResult(w, runs, err)
}
func (h *HTTPServer) handleStartRun(w http.ResponseWriter, r *http.Request) {
	var request startRequest
	if err := decodeBody(w, r, &request); err != nil {
		return
	}
	result, err := h.service.StartRun(request.Prompt, request.RunID)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}
func (h *HTTPServer) handleCancel(w http.ResponseWriter, _ *http.Request) {
	writeResult(w, map[string]bool{"cancelled": true}, h.service.CancelRun())
}
func (h *HTTPServer) handleEvents(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("runID")
	after, err := parseUintQuery(r, "after", 0)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	limitValue, err := parseUintQuery(r, "limit", 500)
	if err != nil || limitValue > 4096 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("limit must be 0-4096"))
		return
	}
	events, err := h.service.Snapshot(runID, after, int(limitValue))
	writeResult(w, events, err)
}
func (h *HTTPServer) handlePermissions(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.service.PendingPermissions())
}
func (h *HTTPServer) handlePermission(w http.ResponseWriter, r *http.Request) {
	var request permissionDecisionRequest
	if err := decodeBody(w, r, &request); err != nil {
		return
	}
	writeResult(w, map[string]bool{"recorded": true}, h.service.ResolvePermission(r.PathValue("permissionID"), request.Allowed, request.Always))
}
func (h *HTTPServer) handleGateway(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.service.GatewayStatus())
}
func (h *HTTPServer) handleDrain(w http.ResponseWriter, r *http.Request) {
	var request drainRequest
	if err := decodeBody(w, r, &request); err != nil {
		return
	}
	writeResult(w, map[string]bool{"draining": request.Draining}, h.service.SetDrain(r.PathValue("nodeID"), request.Draining))
}

func decodeBody(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body"))
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid trailing JSON"))
		return fmt.Errorf("invalid trailing JSON")
	}
	return nil
}
func parseUintQuery(r *http.Request, key string, fallback uint64) (uint64, error) {
	value := r.URL.Query().Get(key)
	if value == "" {
		return fallback, nil
	}
	var parsed uint64
	_, err := fmt.Sscanf(value, "%d", &parsed)
	return parsed, err
}
func writeResult(w http.ResponseWriter, result any, err error) {
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
