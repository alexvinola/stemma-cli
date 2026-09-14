package store

import (
	"reflect"
	"testing"
)

func fuzzEntityDecoder(f *testing.F, kind string, decode entityDecoder, valid string) {
	for _, seed := range []string{
		valid, "", "Plain text, no front matter.\n", "---\n", "---\n---\nBody\n",
		"---\ntitle: unfinished\n", "---\nenabled: [false]\nextensions: [broken]\n---\nBody\n",
		"---\nactivation:\n  type: path-scoped\n  include: [true, 42]\n---\nBody\n",
		"---\nname: []\nallowedTools: [read, {}]\ntools: 42\nextensions:\n  claude: false\n---\nBody\n",
		"---\nextensions:\n  z: []\n  a: false\n---\nBody\n", "\xff\x00",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		path := ".stemma/entities/fuzz.md"
		// A panic is automatically a fuzz failure. Also guard determinism and the
		// file attribution needed by every CLI command consuming these decoders.
		entity, diags := decode(kind+".fuzz", path, data)
		again, nextDiags := decode(kind+".fuzz", path, data)
		if !reflect.DeepEqual(entity, again) || !reflect.DeepEqual(diags, nextDiags) {
			t.Fatal("entity decoding is not deterministic")
		}
		for _, d := range diags {
			if d.Path != path {
				t.Fatalf("diagnostic lost its source path: %+v", d)
			}
		}
	})
}

func FuzzDecodeContext(f *testing.F) {
	fuzzEntityDecoder(f, "context", decoderOf(DecodeContext), EncodeContext(richProject().ContextDocuments[0]))
}
func FuzzDecodeRule(f *testing.F) {
	fuzzEntityDecoder(f, "rule", decoderOf(DecodeRule), EncodeRule(richProject().Rules[0]))
}
func FuzzDecodeProcedure(f *testing.F) {
	fuzzEntityDecoder(f, "procedure", decoderOf(DecodeProcedure), EncodeProcedure(richProject().Procedures[0]))
}
func FuzzDecodeSkill(f *testing.F) {
	fuzzEntityDecoder(f, "skill", decoderOf(DecodeSkill), EncodeSkill(richProject().Skills[0]))
}
func FuzzDecodeAgent(f *testing.F) {
	fuzzEntityDecoder(f, "agent", decoderOf(DecodeAgent), EncodeAgent(richProject().Agents[0]))
}
func FuzzDecodeDecision(f *testing.F) {
	fuzzEntityDecoder(f, "decision", decoderOf(DecodeDecision), EncodeDecision(richProject().Decisions[0]))
}
