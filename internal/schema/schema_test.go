package schema

import "testing"

func TestFields(t *testing.T) {
	tests := []struct {
		path []string
		want []string // must be present
	}{
		{nil, []string{"package", "pipeline", "subpackages", "vars", "var-transforms", "update", "test", "data"}},
		{[]string{"package"}, []string{"name", "version", "epoch", "copyright", "dependencies", "test-resources", "timeout"}},
		{[]string{"pipeline", "0"}, []string{"uses", "with", "runs", "name", "if", "pipeline", "working-directory", "needs"}},
		{[]string{"pipeline", "0", "needs"}, []string{"packages"}},
		{[]string{"subpackages", "0", "test", "pipeline", "0"}, []string{"uses", "runs"}},
		{[]string{"update", "github"}, []string{"identifier", "strip-prefix", "use-tag"}},
	}
	for _, tt := range tests {
		got := map[string]Field{}
		for _, f := range Fields(tt.path) {
			got[f.Name] = f
		}
		for _, w := range tt.want {
			if _, ok := got[w]; !ok {
				t.Errorf("Fields(%v) missing %q", tt.path, w)
			}
		}
	}
	if Fields([]string{"pipeline"}) != nil {
		t.Error("an array without an index has no fields")
	}
}

func TestLookup(t *testing.T) {
	f, def, ok := Lookup([]string{"package", "epoch"})
	if !ok || def != "Package" || !f.Required || f.Type != "integer" || f.Line < 0 {
		t.Errorf("package.epoch = %+v in %s, ok=%v", f, def, ok)
	}
	if f, _, _ := Lookup([]string{"package", "timeout"}); f.Type != "string" {
		t.Errorf("timeout type = %q, want string (duration)", f.Type)
	}
	if f, _, _ := Lookup([]string{"update", "schedule", "period"}); len(f.Enum) != 3 {
		t.Errorf("period enum = %v", f.Enum)
	}
	if f, _, _ := Lookup([]string{"pipeline", "0", "with"}); f.Type != "map" {
		t.Errorf("with type = %q, want map", f.Type)
	}
	if f, _, _ := Lookup([]string{"pipeline", "0"}); f.Def != "Pipeline" || f.Type != "array" {
		t.Errorf("pipeline[0] = %+v", f)
	}
	// The generated schema marks cpe required; melange does not.
	if f, _, _ := Lookup([]string{"package", "cpe"}); f.Required {
		t.Error("package.cpe should not be required")
	}
}

func TestPipelineRoot(t *testing.T) {
	names := map[string]bool{}
	for _, f := range FieldsFrom(RootPipeline, []string{"inputs", "my-input"}) {
		names[f.Name] = true
	}
	for _, w := range []string{"description", "default", "required"} {
		if !names[w] {
			t.Errorf("input fields missing %q: %v", w, names)
		}
	}
	if f, _, ok := LookupFrom(RootPipeline, []string{"needs", "packages"}); !ok || f.Type != "array" {
		t.Errorf("needs.packages = %+v %v", f, ok)
	}
}

func TestDeprecated(t *testing.T) {
	f, _, ok := Lookup([]string{"update", "github", "tag-filter"})
	if !ok || f.Deprecated != "tag-filter-prefix" {
		t.Errorf("tag-filter deprecated = %q, want tag-filter-prefix", f.Deprecated)
	}
}
