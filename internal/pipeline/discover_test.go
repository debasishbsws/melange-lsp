package pipeline

import (
	"os"
	"testing"
)

func TestDiscoverStereo(t *testing.T) {
	root := os.Getenv("STEREO")
	if root == "" {
		t.Skip("set STEREO to a stereo checkout")
	}
	for _, f := range []string{"os/curl.yaml", "enterprise-packages/x.yaml", "extra-packages/x.yaml"} {
		t.Logf("%s: %v", f, DiscoverDirs(root+"/"+f, root))
	}
}
