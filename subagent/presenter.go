package subagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"regexp"
	"strings"

	"github.com/c86j224s/olli/config"
	"github.com/c86j224s/olli/tools"
)

const (
	presenterMaxTitleLength    = 72
	presenterMaxSubtitleLength = 160
	presenterMaxItemLength     = 120
	presenterMaxItems          = 6
)

var presenterFileNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*\.html$`)

type PresentationPlan struct {
	FileName string              `json:"file_name"`
	Template string              `json:"template"`
	Title    string              `json:"title"`
	Subtitle string              `json:"subtitle"`
	Slides   []PresentationSlide `json:"slides"`
}

type PresentationSlide struct {
	Layout   string   `json:"layout"`
	Eyebrow  string   `json:"eyebrow"`
	Title    string   `json:"title"`
	Subtitle string   `json:"subtitle"`
	Items    []string `json:"items"`
	Code     string   `json:"code"`
}

type presenterTemplateSpec struct {
	Name        string
	Label       string
	Description string
	Layouts     []string
	Class       string
}

var presenterTemplateSpecs = []presenterTemplateSpec{
	{Name: config.PresenterTemplateTechnicalEditorial, Label: "Technical Editorial", Description: "Dark engineering narrative for architecture, code, workflows, and evidence", Layouts: []string{"hero", "statement", "pipeline", "cards", "code", "evidence", "closing"}, Class: "theme-technical"},
	{Name: config.PresenterTemplateProductNarrative, Label: "Product Narrative", Description: "Problem-to-proof product story with feature and outcome cards", Layouts: []string{"hero", "problem", "contrast", "pipeline", "cards", "evidence", "closing"}, Class: "theme-product"},
	{Name: config.PresenterTemplateExecutiveBrief, Label: "Executive Brief", Description: "Concise decision deck emphasizing outcomes, metrics, risks, and next actions", Layouts: []string{"hero", "statement", "metrics", "cards", "evidence", "closing"}, Class: "theme-executive"},
	{Name: config.PresenterTemplateMinimalKeynote, Label: "Minimal Keynote", Description: "Large-type visual keynote with one idea per slide and very little text", Layouts: []string{"hero", "statement", "contrast", "metrics", "closing"}, Class: "theme-keynote"},
}

func PresenterTemplateCatalog() []string {
	catalog := make([]string, 0, len(presenterTemplateSpecs))
	for _, spec := range presenterTemplateSpecs {
		catalog = append(catalog, fmt.Sprintf("%s — %s. Layouts: %s", spec.Name, spec.Description, strings.Join(spec.Layouts, ", ")))
	}
	return catalog
}

func (r *SubagentRunner) RunPresenter(task string) (*ResultReport, error) {
	return r.RunPresenterWithTemplateContext(context.Background(), task, config.PresenterTemplateAuto)
}

func (r *SubagentRunner) RunPresenterWithContext(ctx context.Context, task string) (*ResultReport, error) {
	return r.RunPresenterWithTemplateContext(ctx, task, config.PresenterTemplateAuto)
}

func (r *SubagentRunner) RunPresenterWithTemplateContext(ctx context.Context, task, requestedTemplate string) (*ResultReport, error) {
	presenterCfg := config.DefaultPresenterConfig()
	if r.cfg != nil {
		presenterCfg = r.cfg.Presenter
	}
	requestedTemplate = strings.TrimSpace(requestedTemplate)
	if requestedTemplate == "" || requestedTemplate == config.PresenterTemplateAuto {
		requestedTemplate = presenterCfg.DefaultTemplate
	}
	if requestedTemplate == "" {
		requestedTemplate = config.PresenterTemplateAuto
	}
	if !validRequestedPresenterTemplate(requestedTemplate) {
		return nil, fmt.Errorf("unknown presenter template %q", requestedTemplate)
	}
	maxSlides := presenterCfg.MaxSlides
	if maxSlides <= 0 {
		maxSlides = config.DefaultPresenterConfig().MaxSlides
	}

	subID := newSubagentID("presenter")
	templateRule := "Choose the best template from the catalog and explain the choice through the resulting content, not prose."
	if requestedTemplate != config.PresenterTemplateAuto {
		templateRule = fmt.Sprintf("Use template %q exactly.", requestedTemplate)
	}
	sysPrompt := fmt.Sprintf(`You are a specialized Presenter content director. Return one JSON presentation plan; do not write HTML, CSS, or JavaScript.
%s
Template catalog:
- %s
Rules:
- Produce 4-%d concise slides.
- Allowed layouts: hero, statement, problem, contrast, pipeline, cards, code, metrics, evidence, closing.
- Use at most %d items per slide. Keep titles under %d characters, subtitles under %d, and each item under %d.
- Use code only for short labels or commands, never markup.
- file_name must be a simple .html basename with no directories.
- Do not invent metrics or evidence.`, templateRule, strings.Join(PresenterTemplateCatalog(), "\n- "), maxSlides, presenterMaxItems, presenterMaxTitleLength, presenterMaxSubtitleLength, presenterMaxItemLength)

	planRunner := r
	if planRunner.think == nil {
		planRunner = planRunner.withThinking(false)
	}
	roleCtx, cancel := withRoleTimeout(ctx, planRunner.roleBudget(TypePresenter))
	defer cancel()
	report, err := planRunner.executeSubagentLoopWithFormat(roleCtx, subID, string(TypePresenterPlan), task, sysPrompt, planRunner.newRoleRegistry(), presentationPlanSchema(maxSlides), nil, nil)
	if err != nil {
		return nil, err
	}
	if report.Status != "SUCCESS" {
		return report, nil
	}
	plan, validationErr := parseAndValidatePresentationPlan(report.Summary, requestedTemplate, maxSlides)
	if validationErr != nil {
		repaired, repairErr := planRunner.requestStructuredRepair(roleCtx, string(TypePresenterPlan), task, sysPrompt, report.Summary, validationErr, presentationPlanSchema(maxSlides), nil)
		if repairErr != nil {
			return nil, fmt.Errorf("presenter plan remained invalid after one repair: %w", repairErr)
		}
		plan, validationErr = parseAndValidatePresentationPlan(repaired, requestedTemplate, maxSlides)
		if validationErr != nil {
			return nil, fmt.Errorf("presenter plan remained invalid after one repair: %w", validationErr)
		}
	}
	html, err := renderPresentation(*plan)
	if err != nil {
		return nil, err
	}
	if err := validateRenderedPresentationHTML(html, len(plan.Slides)); err != nil {
		return nil, err
	}
	path, existed, ok := artifactCandidatePath(map[string]interface{}{"file_path": plan.FileName}, r.workspace, r.workspaceRoot)
	if !ok {
		return nil, fmt.Errorf("presentation path %q is not safely contained in the workspace", plan.FileName)
	}
	result, err := tools.EditFile(path, "", html, r.workspace, r.workspaceRoot)
	if r.callbacks.OnToolCall != nil {
		r.callbacks.OnToolCall(string(TypePresenter), "render_presentation", map[string]interface{}{"file_path": plan.FileName, "template": plan.Template}, result, err)
	}
	if err != nil {
		return nil, fmt.Errorf("render presentation: %w", err)
	}
	report.Type = string(TypePresenter)
	report.Summary = fmt.Sprintf("Created %s using the %s template with %d slides.", plan.FileName, plan.Template, len(plan.Slides))
	report.ToolCallsRun++
	report.ArtifactFiles = []string{path}
	if !existed {
		report.CreatedFiles = []string{path}
	}
	return report, nil
}

func validRequestedPresenterTemplate(value string) bool {
	if value == config.PresenterTemplateAuto {
		return true
	}
	_, ok := presenterTemplateByName(value)
	return ok
}

func presenterTemplateByName(name string) (presenterTemplateSpec, bool) {
	for _, spec := range presenterTemplateSpecs {
		if spec.Name == name {
			return spec, true
		}
	}
	return presenterTemplateSpec{}, false
}

func parsePresentationPlan(raw string) (*PresentationPlan, error) {
	var plan PresentationPlan
	if err := decodeStrictJSON(extractPlanningJSONObject(raw), &plan); err != nil {
		return nil, fmt.Errorf("presenter output is not valid PresentationPlan JSON: %w", err)
	}
	return &plan, nil
}

func parseAndValidatePresentationPlan(raw, requestedTemplate string, maxSlides int) (*PresentationPlan, error) {
	plan, err := parsePresentationPlan(raw)
	if err != nil {
		return nil, err
	}
	if requestedTemplate != config.PresenterTemplateAuto && plan.Template != requestedTemplate {
		return nil, fmt.Errorf("presenter selected template %q, required %q", plan.Template, requestedTemplate)
	}
	if err := validatePresentationPlan(plan, maxSlides); err != nil {
		return nil, err
	}
	return plan, nil
}

func validatePresentationPlan(plan *PresentationPlan, maxSlides int) error {
	if plan == nil {
		return fmt.Errorf("presentation plan is required")
	}
	plan.FileName = strings.TrimSpace(plan.FileName)
	plan.Template = strings.TrimSpace(plan.Template)
	plan.Title = strings.TrimSpace(plan.Title)
	plan.Subtitle = strings.TrimSpace(plan.Subtitle)
	if !presenterFileNamePattern.MatchString(plan.FileName) {
		return fmt.Errorf("presentation file_name %q must be a simple .html basename", plan.FileName)
	}
	spec, ok := presenterTemplateByName(plan.Template)
	if !ok {
		return fmt.Errorf("presentation template %q is not supported", plan.Template)
	}
	if plan.Title == "" || len([]rune(plan.Title)) > presenterMaxTitleLength {
		return fmt.Errorf("presentation title is required and cannot exceed %d characters", presenterMaxTitleLength)
	}
	if len([]rune(plan.Subtitle)) > presenterMaxSubtitleLength {
		return fmt.Errorf("presentation subtitle cannot exceed %d characters", presenterMaxSubtitleLength)
	}
	if len(plan.Slides) < 4 || len(plan.Slides) > maxSlides {
		return fmt.Errorf("presentation requires 4-%d slides", maxSlides)
	}
	allowedLayouts := make(map[string]struct{}, len(spec.Layouts))
	for _, layout := range spec.Layouts {
		allowedLayouts[layout] = struct{}{}
	}
	for index := range plan.Slides {
		slide := &plan.Slides[index]
		slide.Layout = strings.TrimSpace(slide.Layout)
		slide.Eyebrow = strings.TrimSpace(slide.Eyebrow)
		slide.Title = strings.TrimSpace(slide.Title)
		slide.Subtitle = strings.TrimSpace(slide.Subtitle)
		slide.Code = strings.TrimSpace(slide.Code)
		if _, ok := allowedLayouts[slide.Layout]; !ok {
			return fmt.Errorf("slide %d layout %q is not supported by %s", index+1, slide.Layout, spec.Name)
		}
		if slide.Title == "" || len([]rune(slide.Title)) > presenterMaxTitleLength {
			return fmt.Errorf("slide %d title is required and cannot exceed %d characters", index+1, presenterMaxTitleLength)
		}
		if len([]rune(slide.Subtitle)) > presenterMaxSubtitleLength {
			return fmt.Errorf("slide %d subtitle cannot exceed %d characters", index+1, presenterMaxSubtitleLength)
		}
		if len(slide.Items) > presenterMaxItems {
			return fmt.Errorf("slide %d cannot exceed %d items", index+1, presenterMaxItems)
		}
		for itemIndex, item := range slide.Items {
			slide.Items[itemIndex] = strings.TrimSpace(item)
			if slide.Items[itemIndex] == "" || len([]rune(slide.Items[itemIndex])) > presenterMaxItemLength {
				return fmt.Errorf("slide %d item %d is empty or exceeds %d characters", index+1, itemIndex+1, presenterMaxItemLength)
			}
		}
	}
	return nil
}

func presentationPlanSchema(maxSlides int) map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"file_name", "template", "title", "subtitle", "slides"},
		"properties": map[string]any{
			"file_name": map[string]any{"type": "string", "minLength": 6, "maxLength": 96},
			"template":  map[string]any{"type": "string", "enum": []string{config.PresenterTemplateTechnicalEditorial, config.PresenterTemplateProductNarrative, config.PresenterTemplateExecutiveBrief, config.PresenterTemplateMinimalKeynote}},
			"title":     map[string]any{"type": "string", "minLength": 1, "maxLength": presenterMaxTitleLength},
			"subtitle":  map[string]any{"type": "string", "maxLength": presenterMaxSubtitleLength},
			"slides": map[string]any{"type": "array", "minItems": 4, "maxItems": maxSlides, "items": map[string]any{
				"type": "object", "additionalProperties": false,
				"required": []string{"layout", "eyebrow", "title", "subtitle", "items", "code"},
				"properties": map[string]any{
					"layout":   map[string]any{"type": "string", "enum": []string{"hero", "statement", "problem", "contrast", "pipeline", "cards", "code", "metrics", "evidence", "closing"}},
					"eyebrow":  map[string]any{"type": "string", "maxLength": 48},
					"title":    map[string]any{"type": "string", "minLength": 1, "maxLength": presenterMaxTitleLength},
					"subtitle": map[string]any{"type": "string", "maxLength": presenterMaxSubtitleLength},
					"items":    map[string]any{"type": "array", "maxItems": presenterMaxItems, "items": map[string]any{"type": "string", "minLength": 1, "maxLength": presenterMaxItemLength}},
					"code":     map[string]any{"type": "string", "maxLength": 320},
				},
			}},
		},
	}
}

func renderPresentation(plan PresentationPlan) (string, error) {
	spec, ok := presenterTemplateByName(plan.Template)
	if !ok {
		return "", fmt.Errorf("unknown presentation template %q", plan.Template)
	}
	view := struct {
		PresentationPlan
		ThemeClass string
	}{PresentationPlan: plan, ThemeClass: spec.Class}
	var output bytes.Buffer
	if err := presentationHTMLTemplate.Execute(&output, view); err != nil {
		return "", fmt.Errorf("render presentation template: %w", err)
	}
	return output.String(), nil
}

func validateRenderedPresentationHTML(value string, slideCount int) error {
	checks := []string{"<!doctype html>", `width=device-width, initial-scale=1`, `aria-label="Previous slide"`, `aria-label="Next slide"`, `prefers-reduced-motion`, `case "Home"`, `case "End"`, `case "ArrowLeft"`, `case "ArrowRight"`}
	for _, check := range checks {
		if !strings.Contains(value, check) {
			return fmt.Errorf("rendered presentation is missing %q", check)
		}
	}
	if strings.Count(value, `<section class="slide`) != slideCount {
		return fmt.Errorf("rendered presentation slide count does not match plan")
	}
	for _, forbidden := range []string{"<script src=", "<link rel=", "fetch(", "XMLHttpRequest", "<form", " download="} {
		if strings.Contains(value, forbidden) {
			return fmt.Errorf("rendered presentation contains forbidden construct %q", forbidden)
		}
	}
	return nil
}

var presentationHTMLTemplate = template.Must(template.New("presentation").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>
:root{--bg:#0b0e12;--panel:#141a21;--panel2:#1b2430;--text:#f6f8fb;--muted:#a9b4c2;--accent:#67e8c3;--accent2:#8da7ff;--line:#2d3948;--danger:#ff7d8d;color-scheme:dark}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--text);font-family:Inter,ui-sans-serif,system-ui,-apple-system,"Segoe UI",sans-serif;overflow:hidden}.deck{height:100vh;display:grid;grid-template-rows:1fr auto}.slides{position:relative;min-height:0}.slide{position:absolute;inset:0;padding:clamp(28px,5vw,72px);display:grid;grid-template-columns:minmax(0,.9fr) minmax(0,1.1fr);gap:clamp(24px,4vw,64px);align-items:center;opacity:0;transform:translateX(24px);pointer-events:none;transition:opacity .22s ease,transform .22s ease}.slide.active{opacity:1;transform:none;pointer-events:auto}.theme-product{--bg:#111019;--panel:#201d2d;--panel2:#2a253a;--accent:#ffb86b;--accent2:#ff7fa9;--line:#443b59}.theme-executive{--bg:#0b1420;--panel:#152337;--panel2:#1d2e45;--accent:#f2c66d;--accent2:#88b8ff;--line:#314965}.theme-keynote{--bg:#080808;--panel:#171717;--panel2:#212121;--accent:#fff;--accent2:#b8b8b8;--line:#343434}.copy{max-width:760px}.eyebrow{margin:0 0 16px;color:var(--accent);font-size:.8rem;font-weight:800;letter-spacing:.13em;text-transform:uppercase}.title{margin:0;font-size:clamp(2.5rem,6vw,6.5rem);line-height:.98;letter-spacing:-.055em}.subtitle{margin:22px 0 0;color:var(--muted);font-size:clamp(1rem,1.7vw,1.3rem);line-height:1.55}.visual{display:grid;gap:14px}.items{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:14px;list-style:none;padding:0;margin:0}.item{min-height:112px;padding:18px;border:1px solid var(--line);border-radius:16px;background:linear-gradient(145deg,var(--panel),var(--panel2));display:flex;align-items:flex-end;font-size:1.05rem;line-height:1.4}.pipeline .items{grid-template-columns:repeat(auto-fit,minmax(120px,1fr))}.pipeline .item{position:relative;border-top:4px solid var(--accent)}.pipeline .item:not(:last-child)::after{content:"→";position:absolute;right:-15px;top:50%;color:var(--accent);z-index:2}.metrics .item{font-size:1.35rem;font-weight:800;color:var(--accent)}.problem .item{border-left:4px solid var(--danger)}.codebox{margin:0;padding:22px;border:1px solid var(--line);border-radius:16px;background:#050608;color:var(--accent);font:1rem/1.55 ui-monospace,SFMono-Regular,Menlo,monospace;white-space:pre-wrap;overflow:auto}.hero,.closing,.statement{grid-template-columns:1fr;text-align:center}.hero .copy,.closing .copy,.statement .copy{margin:auto}.hero .visual,.closing .visual,.statement .visual{max-width:880px;margin:auto;width:100%}.controls{display:grid;grid-template-columns:auto 1fr auto;gap:16px;align-items:center;padding:14px 20px;border-top:1px solid var(--line);background:#080b0f}.nav{display:flex;gap:8px}.nav button{min-width:44px;height:40px;border:1px solid var(--line);border-radius:10px;background:var(--panel);color:var(--text);cursor:pointer}.nav button:hover,.nav button:focus-visible{border-color:var(--accent);outline:none}.progress{height:7px;border-radius:99px;background:var(--panel2);overflow:hidden}.progress span{display:block;height:100%;background:var(--accent);transition:width .18s ease}.counter{min-width:72px;text-align:right;color:var(--muted);font-variant-numeric:tabular-nums}
@media(max-width:800px){body{overflow:auto}.deck{min-height:100vh}.slides{min-height:calc(100vh - 69px)}.slide{grid-template-columns:1fr;align-content:center;overflow:auto;padding:32px 24px}.items{grid-template-columns:1fr}.item{min-height:72px}.pipeline .item:not(:last-child)::after{content:"↓";right:50%;top:auto;bottom:-19px}.title{font-size:clamp(2.4rem,13vw,4.2rem)}}
@media(prefers-reduced-motion:reduce){*,*::before,*::after{animation:none!important;transition:none!important;scroll-behavior:auto!important}}
</style>
</head>
<body class="{{.ThemeClass}}">
<main class="deck" aria-label="{{.Title}} presentation">
<div class="slides">
{{range $i,$s:=.Slides}}<section class="slide {{$s.Layout}}{{if eq $i 0}} active{{end}}" aria-hidden="{{if eq $i 0}}false{{else}}true{{end}}">
<div class="copy">{{if $s.Eyebrow}}<p class="eyebrow">{{$s.Eyebrow}}</p>{{end}}<h{{if eq $i 0}}1{{else}}2{{end}} class="title">{{$s.Title}}</h{{if eq $i 0}}1{{else}}2{{end}}>{{if $s.Subtitle}}<p class="subtitle">{{$s.Subtitle}}</p>{{end}}</div>
<div class="visual">{{if $s.Items}}<ul class="items">{{range $s.Items}}<li class="item">{{.}}</li>{{end}}</ul>{{end}}{{if $s.Code}}<pre class="codebox"><code>{{$s.Code}}</code></pre>{{end}}</div>
</section>{{end}}
</div>
<footer class="controls"><div class="nav"><button id="prev" type="button" aria-label="Previous slide">←</button><button id="next" type="button" aria-label="Next slide">→</button></div><div class="progress" role="progressbar" aria-label="Presentation progress" aria-valuemin="1" aria-valuemax="{{len .Slides}}" aria-valuenow="1"><span id="bar"></span></div><div id="counter" class="counter" aria-live="polite">1 / {{len .Slides}}</div></footer>
</main>
<script>
const slides=[...document.querySelectorAll(".slide")],counter=document.getElementById("counter"),bar=document.getElementById("bar"),progress=document.querySelector(".progress");let index=0;
function render(){slides.forEach((slide,i)=>{const active=i===index;slide.classList.toggle("active",active);slide.setAttribute("aria-hidden",String(!active))});counter.textContent=(index+1)+" / "+slides.length;bar.style.width=(((index+1)/slides.length)*100)+"%";progress.setAttribute("aria-valuenow",String(index+1));document.title=(index+1)+"/"+slides.length+" · {{.Title}}"}
function move(delta){index=Math.max(0,Math.min(slides.length-1,index+delta));render()}
document.getElementById("prev").addEventListener("click",()=>move(-1));document.getElementById("next").addEventListener("click",()=>move(1));
window.addEventListener("keydown",event=>{switch(event.key){case "ArrowRight":case " ":event.preventDefault();move(1);break;case "ArrowLeft":move(-1);break;case "Home":index=0;render();break;case "End":index=slides.length-1;render();break}});render();
</script>
</body>
</html>`))

func marshalPresentationPlan(plan PresentationPlan) string {
	encoded, _ := json.Marshal(plan)
	return string(encoded)
}
