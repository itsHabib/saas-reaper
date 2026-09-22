package ledger

import (
	"bufio"
	"encoding/json"
	"os"
	"testing"
)

// canonicalVector is one shared contract case. The same file drives
// verifier/verify_test.py, so a change to either implementation's accepted
// domain fails on one side and is caught before it can diverge silently.
type canonicalVector struct {
	Name      string  `json:"name"`
	Input     string  `json:"input"`
	Canonical *string `json:"canonical"`
}

func loadCanonicalVectors(t *testing.T) []canonicalVector {
	t.Helper()
	file, err := os.Open("../../fixtures/canonical-vectors.jsonl")
	if err != nil {
		t.Fatalf("open shared vectors: %v", err)
	}
	defer func() { _ = file.Close() }()
	var vectors []canonicalVector
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		if len(scanner.Bytes()) == 0 {
			continue
		}
		var vector canonicalVector
		if err := json.Unmarshal(scanner.Bytes(), &vector); err != nil {
			t.Fatalf("decode vector: %v", err)
		}
		vectors = append(vectors, vector)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	if len(vectors) == 0 {
		t.Fatal("shared vector file is empty")
	}
	return vectors
}

func TestSharedCanonicalVectors(t *testing.T) {
	for _, vector := range loadCanonicalVectors(t) {
		t.Run(vector.Name, func(t *testing.T) {
			got, err := CanonicalValue(json.RawMessage(vector.Input))
			if vector.Canonical == nil {
				if err == nil {
					t.Fatalf("accepted %s as %s; the Python verifier rejects it", vector.Input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("rejected %s (%v); the Python verifier canonicalizes it to %s",
					vector.Input, err, *vector.Canonical)
			}
			if string(got) != *vector.Canonical {
				t.Fatalf("input %s\n got %s\nwant %s", vector.Input, got, *vector.Canonical)
			}
		})
	}
}
