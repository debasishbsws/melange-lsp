package document

import (
	"strings"
	"testing"

	"go.lsp.dev/protocol"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name, uri, text string
		want            Kind
	}{
		{"config", "file:///os/foo.yaml", "package:\n  name: foo\n  version: 1\n", KindConfig},
		{"config without version", "file:///os/foo.yaml", "package:\n  name: foo\n", KindConfig},
		{"package without name", "file:///x.yaml", "package:\n  version: 1\n", KindUnknown},
		{"package scalar", "file:///x.yaml", "package: foo\n", KindUnknown},
		{"github workflow", "file:///.github/workflows/ci.yaml", "name: ci\non: push\njobs:\n  a: {}\n", KindUnknown},
		{"pombump deps", "file:///os/sel/pombump-deps.yaml", "patches:\n  - groupId: x\n", KindUnknown},
		{"cargobump deps", "file:///os/d/cargobump-deps.yaml", "packages:\n  - name: x\n", KindUnknown},
		{"pipeline file", "file:///pipelines/go/build.yaml", "name: Go build\nneeds:\n  packages: [go]\ninputs:\n  x: {}\npipeline:\n  - runs: go build\n", KindPipeline},
		{"pipeline shape outside pipelines dir", "file:///p.yaml", "name: x\nruns: echo\n", KindPipeline},
		{"broken config keeps kind", "file:///os/foo.yaml", "package:\n  name: foo\n  version: [\npipeline:\n", KindConfig},
		{"broken pipeline in pipelines dir", "file:///pipelines/x.yaml", "name: x\nruns: [\n", KindPipeline},
		{"broken unrelated", "file:///x.yaml", "foo: [\n", KindUnknown},
		{"empty", "file:///x.yaml", "", KindUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := New(protocol.DocumentURI(tt.uri), 1, tt.text).Kind; got != tt.want {
				t.Errorf("Kind = %v, want %v", got, tt.want)
			}
		})
	}
}

func rng(sl, sc, el, ec uint32) protocol.Range {
	return protocol.Range{Start: protocol.Position{Line: sl, Character: sc}, End: protocol.Position{Line: el, Character: ec}}
}

func TestRanges(t *testing.T) {
	text := `package:
  name: "é🤖x"
  epoch: 0 # comment
pipeline:
  - runs: |
      make

      make install
  - uses: fetch
    with: {uri: a, expected-sha256: b}
`
	d := New("file:///f.yaml", 1, text)
	if d.Root == nil {
		t.Fatal(d.ParseErr)
	}
	pkg := MapValue(d.Root, "package")
	// é is one UTF-16 unit, 🤖 is two, plus quotes and x: 6 units.
	if got, want := d.Range(MapValue(pkg, "name")), rng(1, 8, 1, 14); got != want {
		t.Errorf("quoted scalar = %v, want %v", got, want)
	}
	if got, want := d.Range(MapValue(pkg, "epoch")), rng(2, 9, 2, 10); got != want {
		t.Errorf("plain scalar = %v, want %v", got, want)
	}
	steps := Items(MapValue(d.Root, "pipeline"))
	if got, want := d.Range(MapValue(steps[0], "runs")), rng(4, 10, 7, 18); got != want {
		t.Errorf("block scalar = %v, want %v", got, want)
	}
	if got, want := d.Range(MapValue(steps[1], "with")), rng(9, 10, 9, 38); got != want {
		t.Errorf("flow mapping = %v, want %v", got, want)
	}
	if got, want := d.ItemRange(steps[1]), rng(8, 2, 9, 38); got != want {
		t.Errorf("item = %v, want %v", got, want)
	}
	if got, want := d.Range(pkg), rng(1, 2, 2, 10); got != want {
		t.Errorf("mapping = %v, want %v", got, want)
	}
}

func TestLocate(t *testing.T) {
	text := `package:
  name: foo
pipeline:
  - uses: fetch
    with:
      uri: https://x
`
	d := New("file:///f.yaml", 1, text)
	tests := []struct {
		line, char uint32
		path       string
		onKey      bool
		value      string
	}{
		{1, 3, "package.name", true, "name"},
		{1, 8, "package.name", false, "foo"},
		{3, 5, "pipeline[0].uses", true, "uses"},
		{3, 12, "pipeline[0].uses", false, "fetch"},
		{5, 8, "pipeline[0].with.uri", true, "uri"},
		{5, 14, "pipeline[0].with.uri", false, "https://x"},
	}
	for _, tt := range tests {
		loc := d.Locate(protocol.Position{Line: tt.line, Character: tt.char})
		if loc == nil {
			t.Errorf("%d:%d: nil location", tt.line, tt.char)
			continue
		}
		if loc.Path.String() != tt.path || loc.OnKey != tt.onKey || loc.Node.Value != tt.value {
			t.Errorf("%d:%d = %s onKey=%v %q, want %s onKey=%v %q", tt.line, tt.char, loc.Path, loc.OnKey, loc.Node.Value, tt.path, tt.onKey, tt.value)
		}
	}
}

func TestContext(t *testing.T) {
	// "§" marks the cursor.
	tests := []struct {
		name, text string
		path       string
		inValue    bool
		word       string
		siblings   string
	}{{
		name: "top level",
		text: "package:\n  name: foo\n§",
		path: "",
	}, {
		name:     "package key",
		text:     "package:\n  name: foo\n  ver§\n  epoch: 0\n",
		path:     "package",
		word:     "ver",
		siblings: "epoch,name",
	}, {
		name:    "value of uses",
		text:    "pipeline:\n  - uses: go/§\n",
		path:    "pipeline[0].uses",
		inValue: true,
		word:    "go/",
	}, {
		name:     "with block of second step",
		text:     "pipeline:\n  - runs: |\n      make\n  - uses: fetch\n    with:\n      uri: x\n      §\n",
		path:     "pipeline[1].with",
		siblings: "uri",
	}, {
		name:     "sibling of uses in item",
		text:     "pipeline:\n  - uses: fetch\n    §\n",
		path:     "pipeline[0]",
		siblings: "uses",
	}, {
		name: "new item",
		text: "pipeline:\n  - uses: a\n  - uses: b\n  - §\n",
		path: "pipeline[2]",
	}, {
		name:     "same-indent sequence",
		text:     "subpackages:\n- name: foo-doc\n  pipeline:\n  - uses: split/manpages\n    §\n",
		path:     "subpackages[0].pipeline[0]",
		siblings: "uses",
	}, {
		name:     "second subpackage",
		text:     "subpackages:\n  - name: a\n    description: x\n  - name: b\n    §\n",
		path:     "subpackages[1]",
		siblings: "name",
	}, {
		name: "nested test pipeline",
		text: "subpackages:\n  - name: a\n    test:\n      pipeline:\n        - uses: test/x\n          with:\n            §\n",
		path: "subpackages[0].test.pipeline[0].with",
	}, {
		name: "in comment",
		text: "package:\n  # @ag§\n",
		path: "",
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i := strings.Index(tt.text, "§")
			text := tt.text[:i] + tt.text[i+len("§"):]
			line := strings.Count(tt.text[:i], "\n")
			char := i - strings.LastIndex(tt.text[:i], "\n") - 1
			d := New("file:///f.yaml", 1, text)
			ctx := d.Context(protocol.Position{Line: uint32(line), Character: uint32(char)})

			if ctx.Path.String() != tt.path {
				t.Errorf("path = %q, want %q", ctx.Path, tt.path)
			}
			if ctx.InValue != tt.inValue {
				t.Errorf("inValue = %v, want %v", ctx.InValue, tt.inValue)
			}
			if tt.word != "" && ctx.Word != tt.word {
				t.Errorf("word = %q, want %q", ctx.Word, tt.word)
			}
			if tt.siblings != "" {
				var got []string
				for k := range ctx.Siblings {
					got = append(got, k)
				}
				if strings.Join(sorted(got), ",") != tt.siblings {
					t.Errorf("siblings = %v, want %s", sorted(got), tt.siblings)
				}
			}
		})
	}
}

func sorted(s []string) []string {
	for i := range s {
		for j := i + 1; j < len(s); j++ {
			if s[j] < s[i] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
	return s
}
