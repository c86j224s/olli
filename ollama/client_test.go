package ollama

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOptionsCanExplicitlyDisablePredictionLimit(t *testing.T) {
	zero := 0
	encoded, err := json.Marshal(Options{NumCtx: 4096, NumPredict: &zero})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"num_predict":0`) {
		t.Fatalf("explicit unlimited prediction setting was omitted: %s", encoded)
	}
}

func TestOptionsOmitUnsetPredictionLimit(t *testing.T) {
	encoded, err := json.Marshal(Options{NumCtx: 4096})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "num_predict") {
		t.Fatalf("unset prediction limit was serialized: %s", encoded)
	}
}
