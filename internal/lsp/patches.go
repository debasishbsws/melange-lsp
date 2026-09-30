package lsp

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"melange-lsp/internal/document"
	"melange-lsp/internal/model"

	"go.lsp.dev/protocol"
	"gopkg.in/yaml.v3"
)

// buildDir is where melange copies the source directory and runs steps.
const buildDir = "/home/build"

// patchRef is one file named in a patches: input.
type patchRef struct {
	Name  string
	Range protocol.Range
	// File is the resolved path on disk, or "" if not found.
	File string
	// Tried are the paths searched, for error messages.
	Tried []string
}

// sourceDir is melange's --source-dir as stereo and wolfi-dev/os set it:
// ./<package-name> next to the configuration.
func sourceDir(snap *Snapshot) string {
	name := document.MapValue(document.MapValue(snap.Doc.Root, "package"), "name")
	if name == nil {
		return ""
	}
	return filepath.Join(filepath.Dir(document.Filename(snap.Doc.URI)), name.Value)
}

// workingDir is the step's working-directory relative to the source
// directory, inherited from enclosing steps.
func workingDir(s *model.Step) string {
	for p := s; p != nil; p = p.Parent {
		if wd := document.MapValue(p.Node, "working-directory"); wd != nil && wd.Value != "" {
			return strings.TrimPrefix(strings.TrimPrefix(wd.Value, buildDir), "/")
		}
	}
	return ""
}

var patchToken = regexp.MustCompile(`[^\s"']+`)

// patchRefs resolves the files named in a step's patches: input. Patch
// paths are relative to the step's working directory inside the build
// workspace, which is the package's source directory.
func patchRefs(snap *Snapshot, s *model.Step) []patchRef {
	n := document.MapValue(s.With, "patches")
	src := sourceDir(snap)
	if n == nil || n.Kind != yaml.ScalarNode || src == "" {
		return nil
	}
	d := snap.Doc
	wd := workingDir(s)
	if strings.Contains(wd, "${{") {
		wd = ""
	}

	var out []patchRef
	r := d.Range(n)
	for line := int(r.Start.Line); line <= int(r.End.Line); line++ {
		text := d.Line(line)
		from := 0
		if line == int(r.Start.Line) {
			from = d.ByteOffset(r.Start)
		}
		if i := strings.Index(text[from:], " #"); i >= 0 {
			text = text[:from+i]
		}
		for _, m := range patchToken.FindAllStringIndex(text[from:], -1) {
			name := text[from+m[0] : from+m[1]]
			if name == "|" || name == ">" || name == "|-" || name == ">-" {
				continue
			}
			ref := patchRef{Name: name, Range: protocol.Range{Start: d.ByteToPosition(line, from+m[0]), End: d.ByteToPosition(line, from+m[1])}}

			rel := name
			switch {
			case strings.HasPrefix(rel, "${{package.srcdir}}/"):
				rel = strings.TrimPrefix(rel, "${{package.srcdir}}/")
			case strings.HasPrefix(rel, buildDir+"/"):
				rel = strings.TrimPrefix(rel, buildDir+"/")
			case strings.Contains(rel, "${{"), filepath.IsAbs(rel):
				continue // cannot resolve statically
			default:
				rel = filepath.Join(wd, rel)
			}
			for _, cand := range []string{filepath.Join(src, rel), filepath.Join(src, filepath.Base(name))} {
				// Stay within the directory holding the configuration.
				if !strings.HasPrefix(filepath.Clean(cand), filepath.Dir(src)+string(filepath.Separator)) {
					continue
				}
				if len(ref.Tried) > 0 && ref.Tried[len(ref.Tried)-1] == cand {
					continue
				}
				ref.Tried = append(ref.Tried, cand)
				if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
					ref.File = cand
					break
				}
			}
			out = append(out, ref)
		}
	}
	return out
}
