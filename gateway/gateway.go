package gateway

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/c86j224s/olli/config"
	"github.com/c86j224s/olli/ollama"
)

var (
	envNamePattern    = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
	headerNamePattern = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")
)

type NodeStatus struct {
	ID          string        `json:"id"`
	Endpoint    string        `json:"endpoint"`
	Healthy     bool          `json:"healthy"`
	Draining    bool          `json:"draining"`
	Active      int           `json:"active"`
	Limit       int           `json:"limit"`
	Models      []string      `json:"models"`
	Roles       []string      `json:"roles"`
	Latency     time.Duration `json:"latency"`
	Failures    int           `json:"failures"`
	CircuitOpen bool          `json:"circuit_open"`
	LastError   string        `json:"last_error,omitempty"`
}

type node struct {
	id            string
	endpoint      string
	client        *ollama.Client
	models        map[string]struct{}
	allowedModels map[string]struct{}
	roles         map[string]struct{}
	limit         int
	weight        int
	semaphore     chan struct{}
	mu            sync.Mutex
	active        int
	healthy       bool
	draining      bool
	failures      int
	circuitUntil  time.Time
	latency       time.Duration
	lastError     string
}

type Gateway struct {
	nodes            []*node
	failureThreshold int
	cooldown         time.Duration
	healthInterval   time.Duration
	stop             chan struct{}
	stopOnce         sync.Once
	wg               sync.WaitGroup
}

type lease struct {
	gateway *Gateway
	node    *node
	once    sync.Once
}

func New(cfg config.AIGatewayConfig) (*Gateway, error) {
	if !cfg.Enabled {
		return nil, fmt.Errorf("AI gateway is disabled")
	}
	if cfg.Strategy != config.AIGatewayStrategyLeastLoaded {
		return nil, fmt.Errorf("unsupported AI gateway strategy %q", cfg.Strategy)
	}
	if len(cfg.Nodes) == 0 {
		return nil, fmt.Errorf("AI gateway requires at least one node")
	}
	gateway := &Gateway{
		failureThreshold: cfg.FailureThreshold,
		cooldown:         time.Duration(cfg.CooldownSeconds) * time.Second,
		healthInterval:   time.Duration(cfg.HealthIntervalSeconds) * time.Second,
		stop:             make(chan struct{}),
	}
	if gateway.failureThreshold <= 0 {
		gateway.failureThreshold = 3
	}
	if gateway.cooldown <= 0 {
		gateway.cooldown = time.Minute
	}
	if gateway.healthInterval <= 0 {
		gateway.healthInterval = 15 * time.Second
	}
	seen := make(map[string]struct{}, len(cfg.Nodes))
	for _, nodeCfg := range cfg.Nodes {
		n, err := newNode(nodeCfg)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[n.id]; duplicate {
			return nil, fmt.Errorf("duplicate AI gateway node id %q", n.id)
		}
		seen[n.id] = struct{}{}
		gateway.nodes = append(gateway.nodes, n)
	}
	return gateway, nil
}

func newNode(cfg config.AIGatewayNodeConfig) (*node, error) {
	id := strings.TrimSpace(cfg.ID)
	if id == "" {
		return nil, fmt.Errorf("AI gateway node id is required")
	}
	endpoint, err := validateEndpoint(cfg.Endpoint, cfg.InsecureAllowHTTP)
	if err != nil {
		return nil, fmt.Errorf("AI gateway node %q: %w", id, err)
	}
	if cfg.MaxConcurrency <= 0 || cfg.MaxConcurrency > 64 {
		return nil, fmt.Errorf("AI gateway node %q max_concurrency must be between 1 and 64", id)
	}
	if cfg.Weight <= 0 || cfg.Weight > 1000 {
		return nil, fmt.Errorf("AI gateway node %q weight must be between 1 and 1000", id)
	}
	client := ollama.NewClient(endpoint)
	client.HTTPClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client.Headers = make(http.Header)
	if cfg.AuthTokenEnv != "" {
		if !envNamePattern.MatchString(cfg.AuthTokenEnv) {
			return nil, fmt.Errorf("auth_token_env is invalid")
		}
		value := strings.TrimSpace(os.Getenv(cfg.AuthTokenEnv))
		if value == "" {
			return nil, fmt.Errorf("auth token environment %q is empty", cfg.AuthTokenEnv)
		}
		client.Headers.Set("Authorization", "Bearer "+value)
	}
	for header, envName := range cfg.HeadersFromEnv {
		if !validForwardHeader(header) || !envNamePattern.MatchString(envName) {
			return nil, fmt.Errorf("header environment mapping %q is invalid", header)
		}
		value := strings.TrimSpace(os.Getenv(envName))
		if value == "" {
			return nil, fmt.Errorf("header environment %q is empty", envName)
		}
		client.Headers.Set(header, value)
	}
	n := &node{
		id:            id,
		endpoint:      endpoint,
		client:        client,
		models:        stringSet(cfg.Models),
		allowedModels: stringSet(cfg.Models),
		roles:         normalizedRoleSet(cfg.Roles),
		limit:         cfg.MaxConcurrency,
		weight:        cfg.Weight,
		semaphore:     make(chan struct{}, cfg.MaxConcurrency),
		healthy:       true,
	}
	return n, nil
}

func validateEndpoint(raw string, insecureAllowHTTP bool) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("endpoint must be an absolute HTTP(S) URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", fmt.Errorf("endpoint cannot contain credentials, query, fragment, or path")
	}
	if parsed.Hostname() == "" || (parsed.Port() != "" && !validPort(parsed.Port())) {
		return "", fmt.Errorf("endpoint host or port is invalid")
	}
	if ip := net.ParseIP(parsed.Hostname()); ip != nil && (ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()) {
		return "", fmt.Errorf("endpoint IP class is not allowed")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", fmt.Errorf("endpoint scheme must be https or http")
	}
	if parsed.Scheme == "http" && !insecureAllowHTTP && !isLoopbackHost(parsed.Hostname()) {
		return "", fmt.Errorf("non-loopback HTTP endpoint requires insecure_allow_http=true")
	}
	parsed.Path = ""
	return strings.TrimSuffix(parsed.String(), "/"), nil
}

func validPort(value string) bool {
	if value == "" {
		return true
	}
	port, err := strconv.Atoi(value)
	return err == nil && port > 0 && port <= 65535
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validForwardHeader(name string) bool {
	name = strings.TrimSpace(name)
	if !headerNamePattern.MatchString(name) {
		return false
	}
	canonical := http.CanonicalHeaderKey(name)
	switch canonical {
	case "Host", "Content-Length", "Connection", "Transfer-Encoding", "Proxy-Authorization":
		return false
	default:
		return true
	}
}

func stringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			set[value] = struct{}{}
		}
	}
	return set
}

func normalizedRoleSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value = normalizeRole(value); value != "" {
			set[value] = struct{}{}
		}
	}
	return set
}

func normalizeRole(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if index := strings.IndexByte(value, '.'); index >= 0 {
		value = value[:index]
	}
	return value
}

func (g *Gateway) Start(ctx context.Context) {
	if g == nil {
		return
	}
	g.Refresh(ctx)
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		ticker := time.NewTicker(g.healthInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				probeCtx, cancel := context.WithTimeout(context.Background(), minDuration(g.healthInterval, 10*time.Second))
				g.Refresh(probeCtx)
				cancel()
			case <-ctx.Done():
				return
			case <-g.stop:
				return
			}
		}
	}()
}

func (g *Gateway) Close() {
	if g == nil {
		return
	}
	g.stopOnce.Do(func() { close(g.stop) })
	g.wg.Wait()
}

func (g *Gateway) Refresh(ctx context.Context) {
	if g == nil {
		return
	}
	var wg sync.WaitGroup
	for _, n := range g.nodes {
		n := n
		wg.Add(1)
		go func() {
			defer wg.Done()
			probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			started := time.Now()
			models, err := n.client.ListModelsWithContext(probeCtx)
			n.mu.Lock()
			defer n.mu.Unlock()
			n.latency = time.Since(started)
			if err != nil {
				n.healthy = false
				n.lastError = sanitizeError(err)
				return
			}
			n.healthy = true
			n.lastError = ""
			discovered := stringSet(models)
			if len(n.allowedModels) > 0 {
				filtered := make(map[string]struct{}, len(n.allowedModels))
				for model := range discovered {
					if _, allowed := n.allowedModels[model]; allowed {
						filtered[model] = struct{}{}
					}
				}
				n.models = filtered
			} else {
				n.models = discovered
			}
		}()
	}
	wg.Wait()
}

func (g *Gateway) Acquire(ctx context.Context, request ollama.RouteRequest) (ollama.ClientLease, error) {
	if g == nil {
		return nil, fmt.Errorf("AI gateway is nil")
	}
	role := normalizeRole(request.Role)
	model := strings.TrimSpace(request.Model)
	for {
		candidates := g.candidates(role, model)
		if len(candidates) == 0 {
			return nil, fmt.Errorf("no healthy AI gateway node supports role %q and model %q", role, model)
		}
		for _, candidate := range candidates {
			select {
			case candidate.semaphore <- struct{}{}:
				candidate.mu.Lock()
				candidate.active++
				candidate.mu.Unlock()
				return &lease{gateway: g, node: candidate}, nil
			default:
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func (g *Gateway) candidates(role, model string) []*node {
	type scoredNode struct {
		node  *node
		score float64
	}
	now := time.Now()
	var scored []scoredNode
	for _, n := range g.nodes {
		n.mu.Lock()
		healthy := n.healthy
		draining := n.draining
		circuitOpen := now.Before(n.circuitUntil)
		_, modelOK := n.models[model]
		_, roleOK := n.roles[role]
		if len(n.models) == 0 && len(n.allowedModels) == 0 {
			modelOK = true
		}
		if len(n.roles) == 0 || role == "" {
			roleOK = true
		}
		score := float64(n.active+1) / float64(n.limit*n.weight)
		n.mu.Unlock()
		if healthy && !draining && !circuitOpen && modelOK && roleOK {
			scored = append(scored, scoredNode{node: n, score: score})
		}
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score == scored[j].score {
			return scored[i].node.id < scored[j].node.id
		}
		return scored[i].score < scored[j].score
	})
	candidates := make([]*node, 0, len(scored))
	for _, item := range scored {
		candidates = append(candidates, item.node)
	}
	return candidates
}

func (l *lease) Client() ollama.ChatClient { return l.node.client }
func (l *lease) NodeID() string            { return l.node.id }

func (l *lease) Release(runErr error) {
	if l == nil || l.node == nil {
		return
	}
	l.once.Do(func() {
		l.node.mu.Lock()
		if l.node.active > 0 {
			l.node.active--
		}
		if runErr == nil || errors.Is(runErr, context.Canceled) {
			l.node.failures = 0
		} else {
			l.node.failures++
			l.node.lastError = sanitizeError(runErr)
			if l.node.failures >= l.gateway.failureThreshold {
				l.node.circuitUntil = time.Now().Add(l.gateway.cooldown)
			}
		}
		l.node.mu.Unlock()
		<-l.node.semaphore
	})
}

func (g *Gateway) ListModels() ([]string, error) {
	return g.ListModelsWithContext(context.Background())
}

func (g *Gateway) ListModelsWithContext(ctx context.Context) ([]string, error) {
	g.Refresh(ctx)
	set := make(map[string]struct{})
	for _, status := range g.Status() {
		if !status.Healthy || status.Draining || status.CircuitOpen {
			continue
		}
		allowsMain := len(status.Roles) == 0
		for _, role := range status.Roles {
			if role == "main" {
				allowsMain = true
				break
			}
		}
		if !allowsMain {
			continue
		}
		for _, model := range status.Models {
			set[model] = struct{}{}
		}
	}
	models := make([]string, 0, len(set))
	for model := range set {
		models = append(models, model)
	}
	sort.Strings(models)
	if len(models) == 0 {
		return nil, fmt.Errorf("AI gateway has no healthy models")
	}
	return models, nil
}

func (g *Gateway) ChatStreamFull(req ollama.ChatRequest, callbacks ollama.StreamCallbacks) (*ollama.Message, error) {
	return g.ChatStreamFullWithContext(context.Background(), req, callbacks)
}

func (g *Gateway) ChatStreamFullWithContext(ctx context.Context, req ollama.ChatRequest, callbacks ollama.StreamCallbacks) (*ollama.Message, error) {
	acquired, err := g.Acquire(ctx, ollama.RouteRequest{Role: "main", Model: req.Model})
	if err != nil {
		return nil, err
	}
	message, runErr := acquired.Client().ChatStreamFullWithContext(ctx, req, callbacks)
	acquired.Release(runErr)
	return message, runErr
}

func (g *Gateway) Status() []NodeStatus {
	if g == nil {
		return nil
	}
	statuses := make([]NodeStatus, 0, len(g.nodes))
	now := time.Now()
	for _, n := range g.nodes {
		n.mu.Lock()
		models := mapKeys(n.models)
		roles := mapKeys(n.roles)
		status := NodeStatus{ID: n.id, Endpoint: n.endpoint, Healthy: n.healthy, Draining: n.draining, Active: n.active, Limit: n.limit, Models: models, Roles: roles, Latency: n.latency, Failures: n.failures, CircuitOpen: now.Before(n.circuitUntil), LastError: n.lastError}
		n.mu.Unlock()
		statuses = append(statuses, status)
	}
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].ID < statuses[j].ID })
	return statuses
}

func (g *Gateway) StatusText() string {
	statuses := g.Status()
	if len(statuses) == 0 {
		return "AI gateway has no configured nodes."
	}
	var output strings.Builder
	fmt.Fprintln(&output, "NODE\tSTATUS\tACTIVE/LIMIT\tLATENCY\tMODELS\tROLES")
	for _, status := range statuses {
		state := "healthy"
		switch {
		case status.Draining:
			state = "draining"
		case status.CircuitOpen:
			state = "circuit-open"
		case !status.Healthy:
			state = "unhealthy"
		}
		fmt.Fprintf(&output, "%s\t%s\t%d/%d\t%s\t%s\t%s\n", status.ID, state, status.Active, status.Limit, status.Latency.Round(time.Millisecond), strings.Join(status.Models, ","), strings.Join(status.Roles, ","))
	}
	return strings.TrimSpace(output.String())
}

func (g *Gateway) SetDrain(id string, draining bool) error {
	for _, n := range g.nodes {
		if n.id == id {
			n.mu.Lock()
			n.draining = draining
			n.mu.Unlock()
			return nil
		}
	}
	return fmt.Errorf("unknown AI gateway node %q", id)
}

func mapKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func sanitizeError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if len(message) > 240 {
		message = message[:240]
	}
	return message
}

func minDuration(left, right time.Duration) time.Duration {
	if left < right {
		return left
	}
	return right
}
