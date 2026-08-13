package cli

import (
	"encoding/json"
	"testing"
)

func TestDecodeWorkflowInputsPreservesRawJSONRemainder(t *testing.T) {
	inputs, err := decodeWorkflowInputs(`{"prompt":"a red fox in snow","nested":{"quote":"say \"hello\""},"items":[1,2,3]}`)
	if err != nil {
		t.Fatal(err)
	}
	if inputs["prompt"] != "a red fox in snow" {
		t.Fatalf("prompt was split or changed: %#v", inputs["prompt"])
	}
	nested, ok := inputs["nested"].(map[string]any)
	if !ok || nested["quote"] != `say "hello"` {
		t.Fatalf("nested JSON was not preserved: %#v", inputs["nested"])
	}
	items, ok := inputs["items"].([]any)
	if !ok || len(items) != 3 || items[0] != json.Number("1") {
		t.Fatalf("array or numeric representation changed: %#v", inputs["items"])
	}
}

func TestDecodeWorkflowInputsRejectsNonObjectAndTrailingJSON(t *testing.T) {
	for _, value := range []string{`[]`, `null`, `{"ok":true} {"extra":true}`, `{"key":1,"key":2}`, `{"nested":{"key":1,"key":2}}`} {
		if _, err := decodeWorkflowInputs(value); err == nil {
			t.Fatalf("invalid workflow inputs accepted: %s", value)
		}
	}
}

func TestSplitFirstFieldKeepsJSONTail(t *testing.T) {
	first, rest := splitFirstField("  run\timage-generate-verify  {\"prompt\": \"space kept\"}")
	if first != "run" || rest != `image-generate-verify  {"prompt": "space kept"}` {
		t.Fatalf("unexpected split: first=%q rest=%q", first, rest)
	}
}
