package ledger

import (
	"encoding/json"
	"strings"
	"testing"
)

// The expected values below were produced by verifier/verify.py, which shares
// no code with this package. Changing either side without the other is a
// contract break.
const (
	pythonCanonical = `{"action":"plan.changed","actor":"service:billing","id":"evt-0003",` +
		`"metadata":{"alpha":"first","note":"quote \" backslash \\ tab \u0009 line \u000a unicode ünïcödé 日本語 <&>",` +
		`"zeta":{"nested":[1,-2,0,true,false,null]}},"occurredAt":"2026-08-30T10:00:00Z",` +
		`"recordedAt":"2026-08-30T10:00:01.5Z","sequence":3,"source":"demo-ingest",` +
		`"target":"subscription:sub_9f3","tenant":"acme"}`
	pythonPrevious    = "abababababababababababababababababababababababababababababababab"
	pythonHash        = "324f3f3c0c3353adbbd4f90f7eda4ed227ba0644cc8eb58a8cce9a9ef24fcb1d"
	pythonGenesisHash = "62a6cbd003b2dca66420a6fd2eb1ceeeed40a4bcc0e98b029be0aff674d4fe7e"
)

func vectorEntry() Entry {
	return Entry{
		Tenant:     "acme",
		Sequence:   3,
		ID:         "evt-0003",
		Actor:      "service:billing",
		Action:     "plan.changed",
		Target:     "subscription:sub_9f3",
		OccurredAt: "2026-08-30T10:00:00Z",
		RecordedAt: "2026-08-30T10:00:01.5Z",
		Source:     "demo-ingest",
		Metadata: json.RawMessage(`{"zeta": {"nested": [1, -2, -0, true, false, null]}, "alpha": "first",` +
			` "note": "quote \" backslash \\ tab \t line \n unicode ünïcödé 日本語 <&>"}`),
	}
}

func TestCanonicalEntryMatchesPythonVerifier(t *testing.T) {
	canonical, err := CanonicalEntry(vectorEntry())
	if err != nil {
		t.Fatalf("canonical entry: %v", err)
	}
	if string(canonical) != pythonCanonical {
		t.Fatalf("canonical bytes diverge from the Python verifier:\n got %s\nwant %s", canonical, pythonCanonical)
	}
}

func TestLinkMatchesPythonVerifier(t *testing.T) {
	hash, err := Link(pythonPrevious, vectorEntry())
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	if hash != pythonHash {
		t.Fatalf("hash %s diverges from the Python verifier %s", hash, pythonHash)
	}
	genesis, err := Link(GenesisHash, vectorEntry())
	if err != nil {
		t.Fatalf("genesis link: %v", err)
	}
	if genesis != pythonGenesisHash {
		t.Fatalf("genesis hash %s diverges from the Python verifier %s", genesis, pythonGenesisHash)
	}
}

func TestCanonicalValueNormalizes(t *testing.T) {
	cases := map[string]struct {
		raw  string
		want string
	}{
		"absent":         {"", "null"},
		"whitespace":     {"  \n", "null"},
		"null":           {"null", "null"},
		"sorted keys":    {`{"b":1, "a":2}`, `{"a":2,"b":1}`},
		"nested":         {`{"z":{"y":[ {"x":null} ]}}`, `{"z":{"y":[{"x":null}]}}`},
		"negative zero":  {`-0`, `0`},
		"leading plus":   {`[1, 2]`, `[1,2]`},
		"controls":       {`"\u0001\u001f"`, `"\u0001\u001f"`},
		"raw unicode":    {`"é😀"`, `"é😀"`},
		"slash unescape": {`"a\/b"`, `"a/b"`},
		"html raw":       {`"<&>"`, `"<&>"`},
		"empty shapes":   {`{"a":{},"b":[]}`, `{"a":{},"b":[]}`},
		"safe max":       {`9007199254740991`, `9007199254740991`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := CanonicalValue(json.RawMessage(tc.raw))
			if err != nil {
				t.Fatalf("canonical value: %v", err)
			}
			if string(got) != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestCanonicalValueRejects(t *testing.T) {
	deep := strings.Repeat("[", MaxMetadataDepth+2) + strings.Repeat("]", MaxMetadataDepth+2)
	cases := map[string]string{
		"float":            `1.5`,
		"exponent":         `1e2`,
		"unsafe max":       `9007199254740992`,
		"unsafe min":       `-9007199254740992`,
		"multiple values":  `{} {}`,
		"trailing garbage": `{}x`,
		"lone surrogate":   `"\ud800"`,
		"replacement char": `"�"`,
		"too deep":         deep,
		"too large":        `"` + strings.Repeat("a", MaxMetadataBytes) + `"`,
		"invalid json":     `{"a":}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := CanonicalValue(json.RawMessage(raw)); err == nil {
				t.Fatalf("accepted %s", raw)
			}
		})
	}
}

func TestVerifyDetectsBreakage(t *testing.T) {
	first := chained(t, GenesisHash, 1, `{"n":1}`)
	second := chained(t, first.Hash, 2, `{"n":2}`)
	third := chained(t, second.Hash, 3, `{"n":3}`)
	head, err := Verify([]Entry{first, second, third})
	if err != nil {
		t.Fatalf("intact chain: %v", err)
	}
	if head.Sequence != 3 || head.Hash != third.Hash {
		t.Fatalf("head %+v does not match the last entry", head)
	}
	tampered := second
	tampered.Metadata = json.RawMessage(`{"n":22}`)
	head, err = Verify([]Entry{first, tampered, third})
	if err == nil || !strings.Contains(err.Error(), "sequence 2 hash mismatch") {
		t.Fatalf("tampered chain verified: head %+v err %v", head, err)
	}
	if head.Sequence != 1 {
		t.Fatalf("tampered chain head %+v, want the last good position 1", head)
	}
	if _, err := Verify([]Entry{first, third}); err == nil || !strings.Contains(err.Error(), "sequence 3 follows 1") {
		t.Fatalf("gap accepted: %v", err)
	}
	if _, err := Verify([]Entry{second}); err == nil {
		t.Fatal("chain not starting at one was accepted")
	}
}

func TestLinkRejectsMalformedPreviousHash(t *testing.T) {
	for _, previous := range []string{"", "abc", strings.ToUpper(pythonPrevious), strings.Repeat("z", 64)} {
		if _, err := Link(previous, vectorEntry()); err == nil {
			t.Fatalf("accepted previous hash %q", previous)
		}
	}
}

func chained(t *testing.T, previous string, sequence int64, metadata string) Entry {
	t.Helper()
	entry := Entry{
		Tenant:       "acme",
		Sequence:     sequence,
		ID:           "evt-" + strings.Repeat("x", int(sequence)),
		Actor:        "user:ada",
		Action:       "thing.done",
		Target:       "thing:1",
		OccurredAt:   "2026-08-30T10:00:00Z",
		RecordedAt:   "2026-08-30T10:00:01Z",
		Source:       "test",
		Metadata:     json.RawMessage(metadata),
		PreviousHash: previous,
	}
	hash, err := Link(previous, entry)
	if err != nil {
		t.Fatalf("link %d: %v", sequence, err)
	}
	entry.Hash = hash
	return entry
}
