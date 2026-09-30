// Command corpus runs every language server feature over real melange
// files and summarises the diagnostics, to catch false positives, panics,
// and slow paths. Usage: go run ./hack/corpus -root ~/stereo 'os/*.yaml'
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"runtime/pprof"
	"sort"
	"strings"
	"time"

	"melange-lsp/internal/lsp"

	"go.lsp.dev/protocol"
)

func main() {
	root := flag.String("root", ".", "workspace root")
	show := flag.Int("show", 3, "examples to print per diagnostic code")
	sevMax := flag.Int("severity", 2, "report diagnostics at or above this severity (1=error, 2=warning, 3=info, 4=hint)")
	cpuprofile := flag.String("cpuprofile", "", "write a CPU profile")
	flag.Parse()
	if *cpuprofile != "" {
		f, err := os.Create(*cpuprofile)
		if err != nil {
			panic(err)
		}
		_ = pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}

	if abs, err := filepath.Abs(*root); err == nil {
		*root = abs
	}
	var files []string
	for _, g := range flag.Args() {
		m, _ := filepath.Glob(filepath.Join(*root, g))
		files = append(files, m...)
	}
	srv := lsp.NewTestServer(*root)

	type example struct{ file, msg string }
	counts := map[string]int{}
	examples := map[string][]example{}
	filesWith := 0
	var slow []string
	var total time.Duration
	panics := 0

	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		start := time.Now()
		uri := protocol.DocumentURI("file://" + f)
		func() {
			defer func() {
				if r := recover(); r != nil {
					panics++
					fmt.Printf("PANIC %s: %v\n%s\n", f, r, debug.Stack())
				}
			}()
			snap := srv.Open(uri, string(data))
			if !snap.Active() {
				counts["(not melange)"]++
				rel, _ := filepath.Rel(*root, f)
				if len(examples["(not melange)"]) < *show {
					examples["(not melange)"] = append(examples["(not melange)"], example{rel, ""})
				}
				return
			}
			diags := lsp.Diagnostics(snap)
			lsp.ExerciseAll(snap)
			hit := false
			for _, d := range diags {
				if int(d.Severity) > *sevMax {
					continue
				}
				code, _ := d.Code.(string)
				counts[code]++
				hit = true
				if len(examples[code]) < *show {
					rel, _ := filepath.Rel(*root, f)
					examples[code] = append(examples[code], example{fmt.Sprintf("%s:%d", rel, d.Range.Start.Line+1), firstLine(d.Message)})
				}
			}
			if hit {
				filesWith++
			}
		}()
		el := time.Since(start)
		total += el
		if el > 300*time.Millisecond {
			slow = append(slow, fmt.Sprintf("%s (%v)", f, el.Round(time.Millisecond)))
		}
	}

	fmt.Printf("%d files, %d with diagnostics, %d panics, %v total, %v/file\n", len(files), filesWith, panics, total.Round(time.Millisecond), (total / time.Duration(max(1, len(files)))).Round(time.Microsecond))
	var codes []string
	for c := range counts {
		codes = append(codes, c)
	}
	sort.Slice(codes, func(i, j int) bool { return counts[codes[i]] > counts[codes[j]] })
	for _, c := range codes {
		fmt.Printf("%6d  %s\n", counts[c], c)
		for _, e := range examples[c] {
			fmt.Printf("          %s  %s\n", e.file, e.msg)
		}
	}
	if len(slow) > 0 {
		fmt.Printf("slow: %s\n", strings.Join(slow, ", "))
	}
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	if len(s) > 150 {
		s = s[:150] + "..."
	}
	return s
}
