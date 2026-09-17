package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/c86j224s/olli/ollama"
	"github.com/c86j224s/olli/tools"
)

const architectPlannerPrompt = `ROLE: Read-only software architect for a small-model development team.

TASK:
Inspect the existing workspace and design a coarse implementation architecture. Define module boundaries by reason to change, not by line count. Prefer several small cohesive files over one growing implementation file when the objective has separable state, domain rules, rendering, I/O, or tests.

RULES:
- Never modify files and never run commands.
- Inspect every existing file that constrains the architecture.
- Return 1-8 ordered work packages. A work package owns one cohesive responsibility and a small set of files.
- Every file has exactly one owning work package. Never repeat a file path in another package; put each _test.go file in the package whose responsibility is testing that behavior.
- New files are allowed when they reduce coder context and have a clear package-level responsibility.
- Existing public APIs and entry points must remain explicit in acceptance criteria.
- Each work package must leave the repository parseable and identify dependencies on earlier work packages.
- Work-package IDs are planning units, not Go package or import boundaries. Files may share one Go package unless the objective requires otherwise; depends_on records implementation order, not runtime control flow.
- Preserve the requested interaction model exactly. Never invent real-time, asynchronous, non-blocking, automatic-tick, concurrency, encapsulation, or package-isolation requirements that the objective did not request.
- A command followed by Enter describes a valid turn-based blocking input loop unless the objective explicitly requires autonomous time progression.
- Cover the entire objective; do not defer requirements. If the objective requests tests, assign concrete _test.go files and observable test acceptance criteria to a work package.
- Use exactly these top-level keys: goal, packages, final_verification. Never use architecture_plan or work_packages.
- Each packages item uses exactly: id, objective, files, depends_on, acceptance.
- Return JSON only, matching ArchitecturePlan.`

const cassandraPrompt = `ROLE: Cassandra, a hostile read-only reviewer of software architecture plans.

TASK:
Try to prove the proposed architecture will fail before implementation starts.

RULES:
- Never modify files and never run commands.
- Inspect the current architecture and original objective, not implementation code or old prose.
- Report only concrete planning defects: missing requirement coverage, oversized work packages, incoherent file boundaries, dependency cycles, impossible intermediate compile states, conflicting ownership, or unverifiable acceptance criteria.
- Work-package IDs are ordered planning units, not Go packages or import boundaries. depends_on records implementation order only. Do not ask to add or remove depends_on edges based on runtime calls or data access. Do not infer a dependency cycle from runtime calls, data flow, or a controller coordinating two earlier work packages; report a cycle only when depends_on itself contains a directed cycle.
- Never strengthen or weaken the objective. Do not demand real-time, asynchronous, non-blocking, automatic gravity/ticks, concurrency, strict encapsulation, interfaces, or package isolation unless the objective explicitly requires them. Never ask to remove an explicitly requested behavior or test as supposedly too stateful, impure, or integration-oriented.
- Treat commands followed by Enter as a valid turn-based blocking interaction model. Waiting for the next Enter-terminated command between turns is correct progress, not a stall or deadlock. Shared Go structs across cohesive files in one package are valid boundaries; do not require explicit interfaces or state-transfer contracts between those files. Input validation never determines whether a game piece can spawn; that is domain-state logic.
- A requirement explicitly requested by the objective must appear in a package's files and acceptance criteria, including concrete _test.go ownership when tests are requested.
- Before reporting a missing requirement or contract, reread the target package's objective and acceptance criteria. If they already state the required outcome, do not report it. Quote the actual absent requirement in failure_scenario; do not claim depends_on can enforce runtime ordering, parameter use, initialization order, or race freedom.
- Return at most 3 findings: the highest-severity actionable defects in the current architecture.
- Do not rewrite the plan. New findings use stable CASSANDRA-* ids and concise required outcomes.
- For every previous unresolved finding id supplied in the task, return exactly one resolved or unresolved finding_resolution with concise current evidence.
- An unresolved previous finding must remain in findings with the same id and counts toward the 3-finding cap. If all prior unresolved findings cannot fit, select up to 3 by priority and set more_suspected=true; omitted prior ids remain unresolved automatically.
- A resolved finding must not remain in findings.
- more_suspected is true only when the 3-finding cap prevented checking or reporting lower-priority concerns; it requests another review after repair.
- passed is true only when findings is empty, all prior findings are resolved, and more_suspected is false.
- Return JSON only, matching ArchitectureReview.`

const detailPlannerPrompt = `ROLE: Read-only detail planner for one approved architecture work package.

TASK:
Expand exactly one work package into minimal coder milestones.

RULES:
- Never modify files and never run commands.
- Return exactly one complete milestone for this work package. The architecture already bounded the responsibility; do not split one file across incremental milestones.
- One milestone performs one cohesive state change or behavior and touches as few files as possible.
- Every milestone must leave touched source parseable and independently suitable for deterministic static preflight.
- The final milestone must satisfy every acceptance criterion of the work package.
- Use the supplied work-package id exactly as package_id. Milestone ids are step-1, step-2, ... within this response.
- Each milestone uses exactly: id, objective, allowed_files, acceptance, verification. allowed_files must be a non-empty subset of the supplied work-package files.
- Use verification: [] for intermediate milestones so semantic tests and reviewers run only after the assembled implementation. The orchestrator supplies final verification.
- Do not repeat the entire architecture or expand another work package.
- Return JSON only, matching DetailPlan.`

type ArchitecturePlan struct {
	Goal              string             `json:"goal"`
	Packages          []ArchitectureWork `json:"packages"`
	FinalVerification []string           `json:"final_verification"`
}

type ArchitectureWork struct {
	ID         string   `json:"id"`
	Objective  string   `json:"objective"`
	Files      []string `json:"files"`
	DependsOn  []string `json:"depends_on"`
	Acceptance []string `json:"acceptance"`
}

type ArchitectureFinding struct {
	ID              string `json:"id"`
	PackageID       string `json:"package_id"`
	Summary         string `json:"summary"`
	FailureScenario string `json:"failure_scenario"`
	RequiredOutcome string `json:"required_outcome"`
}

type ArchitectureFindingResolution struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Evidence string `json:"evidence"`
}

type ArchitectureReview struct {
	Passed             bool                            `json:"passed"`
	Findings           []ArchitectureFinding           `json:"findings"`
	FindingResolutions []ArchitectureFindingResolution `json:"finding_resolutions"`
	MoreSuspected      bool                            `json:"more_suspected"`
	Summary            string                          `json:"summary"`
	DismissedFindings  []ArchitectureFinding           `json:"dismissed_findings,omitempty"`
}

type DetailPlan struct {
	PackageID string     `json:"package_id"`
	Steps     []PlanStep `json:"steps"`
}

type PlanningReport struct {
	Architecture       ArchitecturePlan      `json:"architecture"`
	Reviews            []ArchitectureReview  `json:"reviews"`
	ReviewLimitReached bool                  `json:"review_limit_reached,omitempty"`
	UnresolvedFindings []ArchitectureFinding `json:"unresolved_findings,omitempty"`
}

const maxArchitectRepairs = 3

type architecturePlanningRoles interface {
	PlanArchitecture(context.Context, string) (*DevelopmentPlan, *PlanningReport, error)
}

func architecturePlanSchema() map[string]any {
	stringArray := func(min int) map[string]any {
		return map[string]any{"type": "array", "minItems": min, "items": map[string]any{"type": "string", "minLength": 1}}
	}
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"goal", "packages", "final_verification"},
		"properties": map[string]any{
			"goal":               map[string]any{"type": "string", "minLength": 1},
			"final_verification": stringArray(1),
			"packages": map[string]any{"type": "array", "minItems": 1, "maxItems": 8, "items": map[string]any{
				"type": "object", "additionalProperties": false,
				"required": []string{"id", "objective", "files", "depends_on", "acceptance"},
				"properties": map[string]any{
					"id":        map[string]any{"type": "string", "pattern": `^package-[1-9][0-9]*$`},
					"objective": map[string]any{"type": "string", "minLength": 1},
					"files":     stringArray(1), "depends_on": stringArray(0), "acceptance": stringArray(1),
				},
			}},
		},
	}
}

func architectureReviewSchema() map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"passed", "findings", "finding_resolutions", "more_suspected", "summary"},
		"properties": map[string]any{
			"passed": map[string]any{"type": "boolean"}, "more_suspected": map[string]any{"type": "boolean"}, "summary": map[string]any{"type": "string"},
			"findings": map[string]any{"type": "array", "maxItems": 3, "items": map[string]any{
				"type": "object", "additionalProperties": false,
				"required": []string{"id", "package_id", "summary", "failure_scenario", "required_outcome"},
				"properties": map[string]any{
					"id": map[string]any{"type": "string", "minLength": 1}, "package_id": map[string]any{"type": "string"},
					"summary": map[string]any{"type": "string", "minLength": 1}, "failure_scenario": map[string]any{"type": "string", "minLength": 1},
					"required_outcome": map[string]any{"type": "string", "minLength": 1},
				},
			}},
			"finding_resolutions": map[string]any{"type": "array", "items": map[string]any{
				"type": "object", "additionalProperties": false,
				"required": []string{"id", "status", "evidence"},
				"properties": map[string]any{
					"id":       map[string]any{"type": "string", "minLength": 1},
					"status":   map[string]any{"type": "string", "enum": []string{"resolved", "unresolved"}},
					"evidence": map[string]any{"type": "string", "minLength": 1},
				},
			}},
		},
	}
}

func detailPlanSchema() map[string]any {
	stepSchema := developmentPlanSchema()["properties"].(map[string]any)["steps"].(map[string]any)
	stepSchema["maxItems"] = 1
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"package_id", "steps"},
		"properties": map[string]any{
			"package_id": map[string]any{"type": "string", "minLength": 1},
			"steps":      stepSchema,
		},
	}
}

func decodePlanningJSON(raw string, target any) error {
	return decodeStrictJSON(extractPlanningJSONObject(raw), target)
}

func extractPlanningJSONObject(raw string) string {
	raw = strings.TrimSpace(raw)
	start := strings.IndexByte(raw, '{')
	if start < 0 {
		return trimPlanningFence(raw)
	}
	depth := 0
	inString := false
	escaped := false
	for index := start; index < len(raw); index++ {
		character := raw[index]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if character == '\\' {
				escaped = true
				continue
			}
			if character == '"' {
				inString = false
			}
			continue
		}
		switch character {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return raw[start : index+1]
			}
		}
	}
	return trimPlanningFence(raw)
}

func normalizeArchitectureJSON(raw string) string {
	raw = extractPlanningJSONObject(raw)
	var value map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return raw
	}
	if _, exists := value["packages"]; !exists {
		if packages, found := value["work_packages"]; found {
			value["packages"] = packages
			delete(value, "work_packages")
		}
	}
	if rawVerification, exists := value["final_verification"]; exists {
		var text string
		if json.Unmarshal(rawVerification, &text) == nil {
			value["final_verification"] = mustMarshalPlanningJSON(extractVerificationCommands(text))
		}
	}
	if rawPackages, exists := value["packages"]; exists {
		var packages []map[string]json.RawMessage
		if json.Unmarshal(rawPackages, &packages) == nil {
			idMap := make(map[string]string, len(packages))
			for index := range packages {
				var oldID string
				if json.Unmarshal(packages[index]["id"], &oldID) != nil {
					var numericID int
					if json.Unmarshal(packages[index]["id"], &numericID) == nil {
						oldID = fmt.Sprint(numericID)
					}
				}
				canonical := fmt.Sprintf("package-%d", index+1)
				idMap[oldID] = canonical
				idMap[canonical] = canonical
				packages[index]["id"] = mustMarshalPlanningJSON(canonical)
			}
			for index := range packages {
				if rawDependencies, found := packages[index]["depends_on"]; found {
					var dependencies []any
					if json.Unmarshal(rawDependencies, &dependencies) == nil {
						canonicalDependencies := make([]string, 0, len(dependencies))
						for _, dependency := range dependencies {
							key := fmt.Sprint(dependency)
							if canonical, found := idMap[key]; found {
								canonicalDependencies = append(canonicalDependencies, canonical)
							} else {
								canonicalDependencies = append(canonicalDependencies, key)
							}
						}
						packages[index]["depends_on"] = mustMarshalPlanningJSON(canonicalDependencies)
					}
				}
				if rawAcceptance, found := packages[index]["acceptance"]; found {
					var acceptance string
					if json.Unmarshal(rawAcceptance, &acceptance) == nil {
						packages[index]["acceptance"] = mustMarshalPlanningJSON([]string{acceptance})
					}
				}
			}
			value["packages"] = mustMarshalPlanningJSON(packages)
		}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return raw
	}
	return string(encoded)
}

func trimPlanningFence(raw string) string {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "```") {
		return raw
	}
	lines := strings.Split(raw, "\n")
	if len(lines) >= 3 && strings.HasPrefix(lines[0], "```") && strings.TrimSpace(lines[len(lines)-1]) == "```" {
		return strings.Join(lines[1:len(lines)-1], "\n")
	}
	return raw
}

func mustMarshalPlanningJSON(value any) json.RawMessage {
	encoded, _ := json.Marshal(value)
	return encoded
}

func normalizeArchitectureVerification(commands []string) []string {
	var normalized []string
	for _, command := range commands {
		lower := strings.ToLower(strings.TrimSpace(command))
		switch {
		case strings.HasPrefix(lower, "go_test"), strings.HasPrefix(lower, "go test"):
			normalized = append(normalized, "go_test ./...")
		case strings.HasPrefix(lower, "go_vet"), strings.HasPrefix(lower, "go vet"):
			normalized = append(normalized, "go_vet ./...")
		default:
			normalized = append(normalized, command)
		}
	}
	return uniqueStrings(normalized)
}

func extractVerificationCommands(value string) []string {
	var commands []string
	lower := strings.ToLower(value)
	if strings.Contains(lower, "go_test") || strings.Contains(lower, "go test") {
		commands = append(commands, "go_test ./...")
	}
	if strings.Contains(lower, "go_vet") || strings.Contains(lower, "go vet") {
		commands = append(commands, "go_vet ./...")
	}
	return commands
}

func normalizeArchitecturePackageOrder(plan *ArchitecturePlan) error {
	if plan == nil {
		return fmt.Errorf("architecture is required")
	}
	byID := make(map[string]ArchitectureWork, len(plan.Packages))
	originalOrder := make(map[string]int, len(plan.Packages))
	indegree := make(map[string]int, len(plan.Packages))
	dependents := make(map[string][]string, len(plan.Packages))
	for index, work := range plan.Packages {
		id := strings.TrimSpace(work.ID)
		if id == "" {
			return fmt.Errorf("architecture package id is required")
		}
		if _, duplicate := byID[id]; duplicate {
			return fmt.Errorf("architecture package id %q is duplicated", id)
		}
		work.ID = id
		work.DependsOn = uniqueStrings(work.DependsOn)
		byID[id] = work
		originalOrder[id] = index
		indegree[id] = len(work.DependsOn)
	}
	for _, work := range plan.Packages {
		for _, dependency := range work.DependsOn {
			if _, exists := byID[dependency]; !exists {
				return fmt.Errorf("architecture package %s has unknown dependency %q", work.ID, dependency)
			}
			dependents[dependency] = append(dependents[dependency], work.ID)
		}
	}
	ready := make([]string, 0, len(plan.Packages))
	for id, count := range indegree {
		if count == 0 {
			ready = append(ready, id)
		}
	}
	sort.Slice(ready, func(i, j int) bool { return originalOrder[ready[i]] < originalOrder[ready[j]] })
	ordered := make([]ArchitectureWork, 0, len(plan.Packages))
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		ordered = append(ordered, byID[id])
		for _, dependent := range dependents[id] {
			indegree[dependent]--
			if indegree[dependent] == 0 {
				ready = append(ready, dependent)
				sort.Slice(ready, func(i, j int) bool { return originalOrder[ready[i]] < originalOrder[ready[j]] })
			}
		}
	}
	if len(ordered) != len(plan.Packages) {
		return fmt.Errorf("architecture dependencies contain a cycle")
	}
	idMap := make(map[string]string, len(ordered))
	for index := range ordered {
		idMap[ordered[index].ID] = fmt.Sprintf("package-%d", index+1)
	}
	for index := range ordered {
		ordered[index].ID = idMap[ordered[index].ID]
		for dependencyIndex, dependency := range ordered[index].DependsOn {
			ordered[index].DependsOn[dependencyIndex] = idMap[dependency]
		}
	}
	plan.Packages = ordered
	return nil
}

func validateArchitecturePlan(plan *ArchitecturePlan) error {
	if plan == nil || strings.TrimSpace(plan.Goal) == "" || len(plan.Packages) == 0 || len(plan.Packages) > 8 {
		return fmt.Errorf("architecture requires a goal and 1-8 work packages")
	}
	if err := normalizeArchitecturePackageOrder(plan); err != nil {
		return err
	}
	plan.FinalVerification = normalizeArchitectureVerification(plan.FinalVerification)
	for index, command := range plan.FinalVerification {
		canonical, err := normalizeVerificationCommand(command)
		if err != nil {
			return fmt.Errorf("architecture final verification %q: %w", command, err)
		}
		plan.FinalVerification[index] = canonical
	}
	plan.FinalVerification = ensureCoreFinalVerification(plan.FinalVerification)
	ids := make(map[string]int, len(plan.Packages))
	for index := range plan.Packages {
		work := &plan.Packages[index]
		if work.ID != fmt.Sprintf("package-%d", index+1) {
			return fmt.Errorf("architecture package %q must be ordered as package-%d", work.ID, index+1)
		}
		if strings.TrimSpace(work.Objective) == "" || len(work.Files) == 0 || len(uniqueStrings(work.Acceptance)) == 0 {
			return fmt.Errorf("architecture package %s requires objective, files, and acceptance", work.ID)
		}
		for fileIndex, path := range work.Files {
			normalized, err := normalizePlanPath(path)
			if err != nil {
				return fmt.Errorf("architecture package %s file %q: %w", work.ID, path, err)
			}
			work.Files[fileIndex] = normalized
		}
		work.Files = uniqueStrings(work.Files)
		work.Acceptance = uniqueStrings(work.Acceptance)
		for previousIndex := 0; previousIndex < index; previousIndex++ {
			previous := &plan.Packages[previousIndex]
			for _, currentFile := range work.Files {
				if !containsString(previous.Files, currentFile) {
					continue
				}
				if strings.HasSuffix(strings.ToLower(currentFile), "_test.go") && architectureWorkOwnsTests(*work) && !architectureWorkOwnsTests(*previous) {
					previous.Files = removeString(previous.Files, currentFile)
					continue
				}
				return fmt.Errorf("architecture file %q has conflicting ownership in %s and %s", currentFile, previous.ID, work.ID)
			}
			if len(previous.Files) == 0 {
				return fmt.Errorf("architecture package %s lost all files while resolving test ownership", previous.ID)
			}
		}
		ids[work.ID] = index
		for _, dependency := range uniqueStrings(work.DependsOn) {
			dependencyIndex, exists := ids[dependency]
			if !exists || dependencyIndex >= index {
				return fmt.Errorf("architecture package %s has unordered dependency %q", work.ID, dependency)
			}
		}
		work.DependsOn = uniqueStrings(work.DependsOn)
	}
	return nil
}

func architectureWorkOwnsTests(work ArchitectureWork) bool {
	text := strings.ToLower(work.Objective + " " + strings.Join(work.Acceptance, " "))
	return containsAnyFold(text, "test", "verify", "coverage")
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func removeString(values []string, target string) []string {
	result := values[:0]
	for _, value := range values {
		if value != target {
			result = append(result, value)
		}
	}
	return result
}

func validateArchitectureReview(plan *ArchitecturePlan, previous []ArchitectureFinding, review *ArchitectureReview) error {
	if review == nil {
		return fmt.Errorf("architecture review is required")
	}
	if len(review.Findings) > 3 {
		return fmt.Errorf("architecture review returned %d findings; maximum is 3", len(review.Findings))
	}
	if len(previous) == 0 {
		review.FindingResolutions = nil
	}
	known := map[string]struct{}{"": struct{}{}}
	for _, work := range plan.Packages {
		known[work.ID] = struct{}{}
	}
	previousIDs := make(map[string]struct{}, len(previous))
	for _, finding := range previous {
		previousIDs[finding.ID] = struct{}{}
	}
	seen := make(map[string]struct{})
	keptFindings := review.Findings[:0]
	for index := range review.Findings {
		finding := &review.Findings[index]
		finding.ID = normalizeCassandraID(finding.ID)
		if _, wasPrevious := previousIDs[finding.ID]; wasPrevious && resolutionStatus(review.FindingResolutions, finding.ID) == "resolved" {
			review.DismissedFindings = append(review.DismissedFindings, *finding)
			continue
		}
		if _, duplicate := seen[finding.ID]; duplicate {
			return fmt.Errorf("architecture finding %q is duplicated", finding.ID)
		}
		seen[finding.ID] = struct{}{}
		packageID := strings.ToLower(strings.TrimSpace(finding.PackageID))
		if packageID == "all" || packageID == "global" || packageID == "architecture" || packageID == "n/a" || packageID == "na" || packageID == "none" || packageID == "not_applicable" || packageID == "not applicable" {
			finding.PackageID = ""
		}
		if _, exists := known[finding.PackageID]; !exists {
			return fmt.Errorf("architecture finding %q references unknown package %q", finding.ID, finding.PackageID)
		}
		if strings.TrimSpace(finding.Summary) == "" || strings.TrimSpace(finding.FailureScenario) == "" || strings.TrimSpace(finding.RequiredOutcome) == "" {
			return fmt.Errorf("architecture finding %q is incomplete", finding.ID)
		}
		keptFindings = append(keptFindings, *finding)
	}
	review.Findings = keptFindings
	resolutions := make(map[string]string, len(review.FindingResolutions))
	for index := range review.FindingResolutions {
		resolution := &review.FindingResolutions[index]
		resolution.ID = normalizeCassandraID(resolution.ID)
		if _, exists := previousIDs[resolution.ID]; !exists {
			if len(previousIDs) == 1 && len(review.FindingResolutions) == 1 {
				for onlyID := range previousIDs {
					resolution.ID = onlyID
				}
			} else {
				return fmt.Errorf("architecture resolution references unknown finding %q", resolution.ID)
			}
		}
		if resolution.Status != "resolved" && resolution.Status != "unresolved" {
			return fmt.Errorf("architecture resolution %q has invalid status %q", resolution.ID, resolution.Status)
		}
		if _, duplicate := resolutions[resolution.ID]; duplicate || strings.TrimSpace(resolution.Evidence) == "" {
			return fmt.Errorf("architecture resolution %q is duplicate or lacks evidence", resolution.ID)
		}
		resolutions[resolution.ID] = resolution.Status
	}
	for id := range previousIDs {
		status, exists := resolutions[id]
		if !exists {
			if review.MoreSuspected {
				continue
			}
			return fmt.Errorf("previous architecture finding %q has no resolution", id)
		}
		_, remains := seen[id]
		if status == "unresolved" && !remains {
			if review.MoreSuspected {
				continue
			}
			return fmt.Errorf("unresolved architecture finding %q is missing", id)
		}
		if status == "resolved" && remains {
			return fmt.Errorf("resolved architecture finding %q remains", id)
		}
	}
	shouldPass := len(review.Findings) == 0 && !review.MoreSuspected
	if review.Passed != shouldPass {
		return fmt.Errorf("architecture review passed flag disagrees with findings or more_suspected")
	}
	return nil
}

func resolutionStatus(resolutions []ArchitectureFindingResolution, id string) string {
	id = normalizeCassandraID(id)
	for _, resolution := range resolutions {
		if normalizeCassandraID(resolution.ID) == id {
			return resolution.Status
		}
	}
	return ""
}

func normalizeCassandraID(id string) string {
	id = strings.TrimSpace(id)
	upper := strings.ToUpper(id)
	for _, prefix := range []string{"CASSANDRA-", "CASSANDRA_", "PACKAGE-", "PACKAGE_", "FINDING-", "FINDING_"} {
		if strings.HasPrefix(upper, prefix) {
			suffix := strings.TrimSpace(id[len(prefix):])
			if number, err := strconv.Atoi(suffix); err == nil && number > 0 {
				return fmt.Sprintf("CASSANDRA-%d", number)
			}
		}
	}
	if !strings.HasPrefix(upper, "CASSANDRA-") {
		id = "CASSANDRA-" + id
	}
	return id
}

func validateDetailPlan(work ArchitectureWork, detail *DetailPlan) error {
	if detail == nil || detail.PackageID != work.ID || len(detail.Steps) != 1 {
		return fmt.Errorf("detail plan for %s requires exactly one milestone", work.ID)
	}
	allowed := make(map[string]struct{}, len(work.Files))
	for _, path := range work.Files {
		allowed[path] = struct{}{}
	}
	for index := range detail.Steps {
		step := &detail.Steps[index]
		step.ID = fmt.Sprintf("step-%d", index+1)
		for _, path := range uniqueStrings(step.AllowedFiles) {
			if _, exists := allowed[path]; !exists {
				return fmt.Errorf("detail milestone %d file %q is outside package %s", index+1, path, work.ID)
			}
		}
		step.AllowedFiles = append([]string(nil), work.Files...)
		step.Acceptance = uniqueStrings(append(step.Acceptance, work.Acceptance...))
		if strings.TrimSpace(step.Objective) == "" || len(step.AllowedFiles) == 0 || len(step.Acceptance) == 0 {
			return fmt.Errorf("detail milestone %d is incomplete", index+1)
		}
		for verificationIndex, command := range step.Verification {
			canonical, err := normalizeVerificationCommand(command)
			if err != nil {
				return fmt.Errorf("detail milestone %d verification: %w", index+1, err)
			}
			step.Verification[verificationIndex] = canonical
		}
	}
	return nil
}

func flattenArchitecturePlan(architecture ArchitecturePlan, details []DetailPlan) (*DevelopmentPlan, error) {
	if len(details) != len(architecture.Packages) {
		return nil, fmt.Errorf("detail plan count does not match architecture")
	}
	plan := &DevelopmentPlan{Goal: architecture.Goal, FinalVerification: append([]string(nil), architecture.FinalVerification...)}
	stepIndex := 1
	for index, work := range architecture.Packages {
		detail := details[index]
		if err := validateDetailPlan(work, &detail); err != nil {
			return nil, err
		}
		plan.Files = append(plan.Files, work.Files...)
		for _, step := range detail.Steps {
			step.ID = fmt.Sprintf("step-%d", stepIndex)
			plan.Steps = append(plan.Steps, step)
			stepIndex++
		}
	}
	if len(plan.Steps) > maxPlanSteps {
		return nil, fmt.Errorf("expanded architecture has %d milestones; maximum is %d", len(plan.Steps), maxPlanSteps)
	}
	plan.Files = uniqueStrings(plan.Files)
	if err := validateDevelopmentPlan(plan); err != nil {
		return nil, err
	}
	return plan, nil
}

func registerArchitectTools(reg *tools.Registry, required []requiredToolCall) {
	if len(required) == 1 {
		registerArchitectViewFile(reg)
		return
	}
	registerPlannerTools(reg)
}

func registerArchitectViewFile(reg *tools.Registry) {
	reg.Register(ollama.Tool{Type: "function", Function: ollama.FunctionDef{
		Name: "view_file", Description: "Read the one explicit existing source file",
		Parameters: ollama.FunctionParamSchema{Type: "object", Properties: map[string]ollama.FunctionParamProperty{
			"file_path": {Type: "string", Description: "Workspace-relative source file"},
		}, Required: []string{"file_path"}},
	}}, func(args map[string]interface{}) (string, error) {
		path, _ := args["file_path"].(string)
		path = normalizePlannerToolPath(path, reg.GetWorkspace())
		return tools.ViewFile(path, 0, 0, reg.GetWorkspace(), reg.GetWorkspaceRoot())
	})
}

func (m *ModelTeamRoles) createArchitecture(ctx context.Context, objective string, feedback []ArchitectureFinding) (*ArchitecturePlan, error) {
	plan, err := m.createArchitectureAttempt(ctx, objective, feedback)
	if err == nil {
		return plan, nil
	}
	retryFeedback := append([]ArchitectureFinding(nil), feedback...)
	retryFeedback = append(retryFeedback, ArchitectureFinding{
		ID:              "HOST-OUTPUT-VALIDATION",
		Summary:         "Architect output failed validation",
		FailureScenario: err.Error(),
		RequiredOutcome: "Return a complete ArchitecturePlan with a non-empty goal, 1-8 uniquely owned work packages, valid acyclic dependencies, and non-empty acceptance criteria.",
	})
	plan, retryErr := m.createArchitectureAttempt(ctx, objective, retryFeedback)
	if retryErr != nil {
		return nil, fmt.Errorf("architect output failed validation after one retry: %w", retryErr)
	}
	return plan, nil
}

func (m *ModelTeamRoles) createArchitectureAttempt(ctx context.Context, objective string, feedback []ArchitectureFinding) (*ArchitecturePlan, error) {
	payloadFeedback := compactArchitectureFindings(feedback)
	payload, _ := json.Marshal(map[string]any{"objective": objective, "cassandra_findings": payloadFeedback})
	reg := m.runner.newRoleRegistry()
	requiredViews := requiredPlannerViewCalls(objective)
	if len(feedback) > 0 {
		requiredViews = nil
	}
	if len(feedback) == 0 {
		registerArchitectTools(reg, requiredViews)
	}
	temperature := 0.1
	evidence := &executionEvidence{ProgressMarker: plannerProgressMarker, RequiredCalls: requiredViews}
	evidence.ProgressState = func() string { return evidenceProgressSet(evidence) }
	evidence.CompletionReady = func() bool { return len(evidence.missingRequiredTools()) == 0 }
	callCtx, cancel := withRoleTimeout(ctx, m.runner.roleBudget(TypePlanner))
	defer cancel()
	report, err := m.runner.withModel(m.models.Planner).executeSubagentLoopWithFormat(callCtx, newSubagentID("architect"), string(TypePlanner), string(payload), architectPlannerPrompt, reg, architecturePlanSchema(), &temperature, evidence)
	if err != nil {
		return nil, err
	}
	if report.Status != "SUCCESS" {
		return nil, fmt.Errorf("architect loop %s: %s", report.Termination, report.Summary)
	}
	var plan ArchitecturePlan
	if err := decodePlanningJSON(normalizeArchitectureJSON(report.Summary), &plan); err != nil {
		return nil, err
	}
	if err := validateArchitecturePlan(&plan); err != nil {
		return nil, fmt.Errorf("architect output validation failed: %w", err)
	}
	return &plan, nil
}

func (m *ModelTeamRoles) reviewArchitecture(ctx context.Context, objective string, plan *ArchitecturePlan, previous []ArchitectureFinding) (*ArchitectureReview, error) {
	payload, _ := json.Marshal(map[string]any{
		"objective":           objective,
		"architecture":        plan,
		"previous_unresolved": compactArchitectureFindings(previous),
	})
	reg := tools.NewEmptyRegistry()
	temperature := 0.1
	callCtx, cancel := withRoleTimeout(ctx, m.runner.roleBudget(TypeReviewer))
	defer cancel()
	cassandraRunner := m.runner.withModel(m.models.Cassandra)
	if m.models.ReviewerThinking != nil {
		cassandraRunner = cassandraRunner.withThinking(*m.models.ReviewerThinking)
	}
	report, err := cassandraRunner.executeSubagentLoopWithFormat(callCtx, newSubagentID("cassandra"), string(TypePlanner), string(payload), cassandraPrompt, reg, architectureReviewSchema(), &temperature, nil)
	if err != nil {
		return nil, err
	}
	if report.Status != "SUCCESS" {
		return nil, fmt.Errorf("cassandra loop %s: %s", report.Termination, report.Summary)
	}
	var review ArchitectureReview
	if err := decodePlanningJSON(report.Summary, &review); err != nil {
		return nil, err
	}
	sanitizeArchitectureReview(objective, plan, previous, &review)
	if err := validateArchitectureReview(plan, previous, &review); err != nil {
		return nil, err
	}
	return &review, nil
}

func sanitizeArchitectureReview(objective string, plan *ArchitecturePlan, previous []ArchitectureFinding, review *ArchitectureReview) {
	if review == nil {
		return
	}
	previousIDs := make(map[string]struct{}, len(previous))
	for _, finding := range previous {
		previousIDs[normalizeCassandraID(finding.ID)] = struct{}{}
	}
	kept := review.Findings[:0]
	dismissedIDs := make(map[string]struct{})
	for _, finding := range review.Findings {
		finding.ID = normalizeCassandraID(finding.ID)
		if architectureFindingStrengthensObjective(objective, plan, finding) {
			review.DismissedFindings = append(review.DismissedFindings, finding)
			dismissedIDs[finding.ID] = struct{}{}
			continue
		}
		kept = append(kept, finding)
	}
	review.Findings = kept
	resolutions := review.FindingResolutions[:0]
	resolvedIDs := make(map[string]struct{}, len(review.FindingResolutions))
	for _, resolution := range review.FindingResolutions {
		resolution.ID = normalizeCassandraID(resolution.ID)
		if _, dismissed := dismissedIDs[resolution.ID]; dismissed {
			resolution.Status = "resolved"
			resolution.Evidence = "dismissed because it strengthens the original objective or contradicts the validated dependency graph"
		}
		resolutions = append(resolutions, resolution)
		resolvedIDs[resolution.ID] = struct{}{}
	}
	for id := range dismissedIDs {
		if _, wasPrevious := previousIDs[id]; !wasPrevious {
			continue
		}
		if _, exists := resolvedIDs[id]; exists {
			continue
		}
		resolutions = append(resolutions, ArchitectureFindingResolution{
			ID:       id,
			Status:   "resolved",
			Evidence: "dismissed because it strengthens the original objective or contradicts the validated dependency graph",
		})
	}
	review.FindingResolutions = resolutions
	restoreUnresolvedArchitectureFindings(previous, review)
	if len(review.Findings) == 0 && !review.MoreSuspected {
		review.Passed = true
	}
	if len(review.DismissedFindings) > 0 {
		dismissalSummary := fmt.Sprintf("host dismissed %d out-of-contract finding(s)", len(review.DismissedFindings))
		if strings.TrimSpace(review.Summary) == "" {
			review.Summary = dismissalSummary
		} else {
			review.Summary = strings.TrimSpace(review.Summary) + "; " + dismissalSummary
		}
	}
}

func restoreUnresolvedArchitectureFindings(previous []ArchitectureFinding, review *ArchitectureReview) {
	if review == nil || len(previous) == 0 {
		return
	}
	present := make(map[string]struct{}, len(review.Findings))
	for _, finding := range review.Findings {
		present[normalizeCassandraID(finding.ID)] = struct{}{}
	}
	previousByID := make(map[string]ArchitectureFinding, len(previous))
	for _, finding := range previous {
		finding.ID = normalizeCassandraID(finding.ID)
		previousByID[finding.ID] = finding
	}
	for _, resolution := range review.FindingResolutions {
		id := normalizeCassandraID(resolution.ID)
		if resolution.Status != "unresolved" {
			continue
		}
		if _, exists := present[id]; exists {
			continue
		}
		finding, exists := previousByID[id]
		if !exists {
			continue
		}
		if len(review.Findings) >= 3 {
			review.MoreSuspected = true
			continue
		}
		review.Findings = append(review.Findings, finding)
		present[id] = struct{}{}
	}
	if len(review.Findings) > 0 || review.MoreSuspected {
		review.Passed = false
	}
}

func architectureFindingStrengthensObjective(objective string, plan *ArchitecturePlan, finding ArchitectureFinding) bool {
	text := strings.ToLower(strings.Join([]string{finding.Summary, finding.FailureScenario, finding.RequiredOutcome}, " "))
	objectiveText := strings.ToLower(objective)
	if findingClaimsDependencyCycle(text) && !architectureHasDependencyCycle(plan) {
		return true
	}
	turnBased := strings.Contains(objectiveText, "turn-based") || strings.Contains(objectiveText, "turn based")
	if turnBased && containsAnyFold(text, "real-time", "real time", "non-blocking", "nonblocking", "asynchronous", "automatic gravity", "automatic tick", "raw terminal", "stall", "deadlock", "blocking nature", "blocks indefinitely") {
		return true
	}
	sharedStructsAllowed := strings.Contains(objectiveText, "share") && strings.Contains(objectiveText, "struct")
	if sharedStructsAllowed && containsAnyFold(text, "encapsulation", "getter", "setter", "interface boundary", "package isolation", "public methods", "struct fields", "contract", "data flow", "function signature", "return signature", "explicit input parameter", "authorized to modify", "partitioned", "global state variables", "feed into", "feeds into", "consume the output", "consumes the output") {
		return true
	}
	if containsAnyFold(text, "input failure", "input validation") && containsAnyFold(text, "spawn", "placement failure", "game-over", "game over") {
		return true
	}
	if containsAnyFold(objectiveText, "piece cannot", "cannot spawn", "cannot be placed") && containsAnyFold(text, "any piece", "full top row", "insurmountable stack") {
		return true
	}
	if objectiveRequiresGameOverTest(objectiveText) && containsAnyFold(text, "remove", "cannot be tested", "impossible to test", "not pure", "too stateful", "stateful and loop-dependent", "integration testing context") && containsAnyFold(text, "game-over", "game over") {
		return true
	}
	if containsAnyFold(text, "remove dependency", "add dependency", "dependency should", "incorrectly depends", "dependency flow is incorrect") && !architectureHasDependencyCycle(plan) {
		return true
	}
	if findingClaimsMissingExistingDependency(plan, finding, text) {
		return true
	}
	if findingOutcomeAlreadyCovered(plan, finding) {
		return true
	}
	if findingRuntimeFlowAlreadyCovered(plan, finding) {
		return true
	}
	if containsAnyFold(text, "race condition", "race freedom") && turnBased {
		return true
	}
	return false
}

func findingRuntimeFlowAlreadyCovered(plan *ArchitecturePlan, finding ArchitectureFinding) bool {
	if plan == nil || finding.PackageID == "" {
		return false
	}
	var work *ArchitectureWork
	for index := range plan.Packages {
		if plan.Packages[index].ID == finding.PackageID {
			work = &plan.Packages[index]
			break
		}
	}
	if work == nil {
		return false
	}
	findingText := strings.ToLower(strings.Join([]string{finding.Summary, finding.FailureScenario, finding.RequiredOutcome}, " "))
	if !containsAnyFold(findingText, "sequence", "control flow", "data flow", "feed into", "output", "before calling", "before p", "input ->", "state update") {
		return false
	}
	packageText := strings.ToLower(work.Objective + " " + strings.Join(work.Acceptance, " "))
	concepts := [][]string{
		{"input", "command", "action"},
		{"update state", "state update", "rules", "logic"},
		{"render", "display", "presentation"},
		{"repeat", "loop", "until"},
	}
	matched := 0
	for _, alternatives := range concepts {
		if containsAnyFold(packageText, alternatives...) {
			matched++
		}
	}
	return matched >= 3
}

func findingOutcomeAlreadyCovered(plan *ArchitecturePlan, finding ArchitectureFinding) bool {
	if plan == nil || finding.PackageID == "" {
		return false
	}
	var work *ArchitectureWork
	for index := range plan.Packages {
		if plan.Packages[index].ID == finding.PackageID {
			work = &plan.Packages[index]
			break
		}
	}
	if work == nil {
		return false
	}
	packageText := strings.ToLower(work.Objective + " " + strings.Join(work.Acceptance, " "))
	outcome := strings.ToLower(finding.RequiredOutcome)
	concepts := [][]string{
		{"rotation", "rotate", "rotated"},
		{"boundaries", "boundary", "within the board"},
		{"collision", "collide"},
		{"first piece", "initial piece", "retrieve the first", "initializ"},
		{"gravity", "dropping", "move down"},
		{"game-over", "game over"},
		{"line clear", "clearing full lines", "full-line"},
		{"score", "scoring"},
	}
	matchedConcepts := 0
	coveredConcepts := 0
	for _, alternatives := range concepts {
		if !containsAnyFold(outcome, alternatives...) {
			continue
		}
		matchedConcepts++
		if containsAnyFold(packageText, alternatives...) {
			coveredConcepts++
		}
	}
	return matchedConcepts >= 2 && matchedConcepts == coveredConcepts
}

func findingClaimsMissingExistingDependency(plan *ArchitecturePlan, finding ArchitectureFinding, text string) bool {
	if plan == nil || !containsAnyFold(text, "missing dependency", "no explicit dependency", "add '", "add \"") {
		return false
	}
	var work *ArchitectureWork
	for index := range plan.Packages {
		if plan.Packages[index].ID == finding.PackageID {
			work = &plan.Packages[index]
			break
		}
	}
	if work == nil {
		return false
	}
	for _, dependency := range work.DependsOn {
		if containsAnyFold(text, dependency) {
			return true
		}
	}
	return false
}

func objectiveRequiresGameOverTest(objective string) bool {
	return containsAnyFold(objective, "tests for", "test for", "testing") && containsAnyFold(objective, "game-over", "game over")
}

func findingClaimsDependencyCycle(text string) bool {
	return containsAnyFold(text, "dependency cycle", "circular dependency", "circular import", "cycle risk")
}

func architectureHasDependencyCycle(plan *ArchitecturePlan) bool {
	if plan == nil {
		return false
	}
	dependencies := make(map[string][]string, len(plan.Packages))
	for _, work := range plan.Packages {
		dependencies[work.ID] = work.DependsOn
	}
	visiting := make(map[string]bool, len(dependencies))
	visited := make(map[string]bool, len(dependencies))
	var visit func(string) bool
	visit = func(id string) bool {
		if visiting[id] {
			return true
		}
		if visited[id] {
			return false
		}
		visiting[id] = true
		for _, dependency := range dependencies[id] {
			if visit(dependency) {
				return true
			}
		}
		visiting[id] = false
		visited[id] = true
		return false
	}
	for id := range dependencies {
		if visit(id) {
			return true
		}
	}
	return false
}

func containsAnyFold(value string, candidates ...string) bool {
	value = strings.ToLower(value)
	for _, candidate := range candidates {
		if strings.Contains(value, strings.ToLower(candidate)) {
			return true
		}
	}
	return false
}

func mergeArchitectureUnresolved(previous []ArchitectureFinding, review *ArchitectureReview) []ArchitectureFinding {
	active := make(map[string]ArchitectureFinding, len(previous)+len(review.Findings))
	for _, finding := range previous {
		active[finding.ID] = finding
	}
	for _, resolution := range review.FindingResolutions {
		if resolution.Status == "resolved" {
			delete(active, resolution.ID)
		}
	}
	for _, finding := range review.Findings {
		active[finding.ID] = finding
	}
	ids := make([]string, 0, len(active))
	for id := range active {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]ArchitectureFinding, 0, len(ids))
	for _, id := range ids {
		result = append(result, active[id])
	}
	return result
}

func rewriteArchitectureFindingTargets(findings []ArchitectureFinding, architecture *ArchitecturePlan) []ArchitectureFinding {
	result := append([]ArchitectureFinding(nil), findings...)
	if architecture == nil {
		return result
	}
	byID := make(map[string]ArchitectureWork, len(architecture.Packages))
	for _, work := range architecture.Packages {
		byID[work.ID] = work
	}
	for index := range result {
		work, exists := byID[result[index].PackageID]
		if !exists {
			continue
		}
		files := strings.Join(work.Files, ", ")
		result[index].PackageID = ""
		result[index].RequiredOutcome = fmt.Sprintf("For the work package currently owning [%s]: %s", files, result[index].RequiredOutcome)
	}
	return result
}

func compactArchitectureFindings(findings []ArchitectureFinding) []map[string]string {
	compacted := make([]map[string]string, 0, len(findings))
	for _, finding := range findings {
		compacted = append(compacted, map[string]string{
			"id": finding.ID, "package_id": finding.PackageID, "required_outcome": finding.RequiredOutcome,
		})
	}
	return compacted
}

func (m *ModelTeamRoles) detailArchitectureWork(ctx context.Context, architecture *ArchitecturePlan, work ArchitectureWork) (*DetailPlan, error) {
	payload, _ := json.Marshal(map[string]any{"goal": architecture.Goal, "work_package": work})
	reg := tools.NewEmptyRegistry()
	temperature := 0.1
	callCtx, cancel := withRoleTimeout(ctx, m.runner.roleBudget(TypePlanner))
	defer cancel()
	report, err := m.runner.withModel(m.models.DetailPlanner).executeSubagentLoopWithFormat(callCtx, newSubagentID("detail-planner"), string(TypePlanner), string(payload), detailPlannerPrompt, reg, detailPlanSchema(), &temperature, nil)
	if err != nil {
		return nil, err
	}
	if report.Status != "SUCCESS" {
		return nil, fmt.Errorf("detail planner loop %s: %s", report.Termination, report.Summary)
	}
	var detail DetailPlan
	if err := decodePlanningJSON(report.Summary, &detail); err != nil {
		return nil, err
	}
	if err := validateDetailPlan(work, &detail); err != nil {
		return nil, err
	}
	return &detail, nil
}
