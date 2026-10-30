package newacctmod

import (
	"encoding/json"
	"testing"
)

func TestParseLLMVerdictJSON(t *testing.T) {
	raw := "```json\n{\"verdict\":\"pass\",\"confidence\":0.9,\"reason\":\"ok\"}\n```"
	// reuse judgeLLM parser by extracting like judge.go
	s := raw
	if i := indexByte(s, "{"); i >= 0 {
		if j := lastIndexByte(s, "}"); j > i {
			s = s[i : j+1]
		}
	}
	var v struct {
		Verdict    string  `json:"verdict"`
		Confidence float64 `json:"confidence"`
		Reason     string  `json:"reason"`
	}
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatal(err)
	}
	if v.Verdict != "pass" || v.Confidence != 0.9 {
		t.Fatalf("%+v", v)
	}
}

func indexByte(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func lastIndexByte(s, sub string) int {
	for i := len(s) - len(sub); i >= 0; i-- {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
