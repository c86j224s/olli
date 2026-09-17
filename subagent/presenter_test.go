package subagent

import (
	"strings"
	"testing"

	"github.com/c86j224s/olli/config"
)

func validPresentationPlan() PresentationPlan {
	return PresentationPlan{
		FileName: "deck.html",
		Template: config.PresenterTemplateTechnicalEditorial,
		Title:    "Bounded Teams",
		Subtitle: "From planning to verified delivery",
		Slides: []PresentationSlide{
			{Layout: "hero", Eyebrow: "O.L.L.I.", Title: "Bounded Teams", Subtitle: "Verified delivery"},
			{Layout: "pipeline", Title: "Plan in layers", Items: []string{"Architect", "Cassandra", "Detail Planner"}},
			{Layout: "cards", Title: "Implement safely", Items: []string{"Coder", "Static preflight"}},
			{Layout: "closing", Title: "Ship evidence", Subtitle: "Bounded autonomy"},
		},
	}
}

func TestPresenterTemplateCatalogIncludesFourDistinctDesigns(t *testing.T) {
	catalog := PresenterTemplateCatalog()
	if len(catalog) != 4 {
		t.Fatalf("unexpected Presenter catalog: %v", catalog)
	}
	for _, name := range []string{"technical-editorial", "product-narrative", "executive-brief", "minimal-keynote"} {
		if !strings.Contains(strings.Join(catalog, "\n"), name) {
			t.Fatalf("template %q missing from catalog: %v", name, catalog)
		}
	}
}

func TestRenderPresentationProvidesResponsiveAccessibleNavigation(t *testing.T) {
	plan := validPresentationPlan()
	if err := validatePresentationPlan(&plan, 10); err != nil {
		t.Fatal(err)
	}
	html, err := renderPresentation(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateRenderedPresentationHTML(html, len(plan.Slides)); err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{`width=device-width, initial-scale=1`, `role="progressbar"`, `aria-valuenow`, `@media(max-width:800px)`, `prefers-reduced-motion`} {
		if !strings.Contains(html, required) {
			t.Fatalf("rendered deck lacks %q", required)
		}
	}
}

func TestRenderPresentationEscapesModelContent(t *testing.T) {
	plan := validPresentationPlan()
	plan.Slides[1].Items = []string{`<script>alert("x")</script>`}
	html, err := renderPresentation(plan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html, `<script>alert`) || !strings.Contains(html, `&lt;script&gt;`) {
		t.Fatalf("model content was not escaped: %s", html)
	}
}

func TestValidatePresentationPlanRejectsTraversalAndTemplateLayoutMismatch(t *testing.T) {
	plan := validPresentationPlan()
	plan.FileName = "../deck.html"
	if err := validatePresentationPlan(&plan, 10); err == nil {
		t.Fatal("expected traversal filename rejection")
	}
	plan = validPresentationPlan()
	plan.Template = config.PresenterTemplateMinimalKeynote
	plan.Slides[1].Layout = "pipeline"
	if err := validatePresentationPlan(&plan, 10); err == nil {
		t.Fatal("expected template layout mismatch rejection")
	}
}

func TestParsePresentationPlanIsStrict(t *testing.T) {
	plan := validPresentationPlan()
	raw := marshalPresentationPlan(plan)
	parsed, err := parsePresentationPlan(raw)
	if err != nil || parsed.Template != plan.Template {
		t.Fatalf("valid plan did not parse: %#v %v", parsed, err)
	}
	if _, err := parsePresentationPlan(strings.TrimSuffix(raw, "}") + `,"extra":true}`); err == nil {
		t.Fatal("expected unknown field rejection")
	}
}
