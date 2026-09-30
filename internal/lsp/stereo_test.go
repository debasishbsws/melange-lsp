package lsp

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.lsp.dev/protocol"

	"melange-lsp/internal/document"
)

// TestStereoReferences checks workspace references against a real stereo
// checkout. Set STEREO to run it.
func TestStereoReferences(t *testing.T) {
	root := os.Getenv("STEREO")
	if root == "" {
		t.Skip("set STEREO to a stereo checkout")
	}
	srv := NewTestServer(root)
	srv.workspace = &workspaceIndex{root: root}

	file := filepath.Join(root, "os", "curl.yaml")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Skip(err)
	}
	snap := srv.Open(document.URI(file), string(data))

	var usesPos, namePos *protocol.Position
	for _, s := range snap.Model.Steps {
		if s.Uses != nil && s.Uses.Value == "git-checkout" && usesPos == nil {
			p := snap.Doc.Start(s.Uses)
			usesPos = &p
		}
	}
	if n := document.MapValue(document.MapValue(snap.Doc.Root, "package"), "name"); n != nil {
		p := snap.Doc.Start(n)
		namePos = &p
	}

	start := time.Now()
	refs := srv.workspaceReferences(snap, *usesPos)
	t.Logf("git-checkout: %d uses in %v (index build included)", len(refs), time.Since(start))
	start = time.Now()
	refs = srv.workspaceReferences(snap, *namePos)
	t.Logf("curl dependents: %d in %v", len(refs), time.Since(start))
	if len(refs) == 0 {
		t.Error("curl should have dependents")
	}
}
