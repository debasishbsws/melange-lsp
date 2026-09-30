package lsp

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"melange-lsp/internal/document"
	"melange-lsp/internal/model"
	"melange-lsp/internal/subst"
)

// traceStep is one stage of a value's derivation.
type traceStep struct {
	Label string
	Value string
	// Note explains a stage that changed nothing or stopped the trace.
	Note string
}

// transformTrace explains how a var-transforms entry computes its variable.
type transformTrace struct {
	Name, From, Match, Replace, Result string
	Matched                            bool
	Err                                error
}

// traceTransform evaluates one var-transforms entry like melange's
// PerformVarSubstitutions: substitute from, then regexp ReplaceAllString.
func traceTransform(snap *Snapshot, v *model.Var) *transformTrace {
	if v == nil || v.Transform == nil {
		return nil
	}
	get := func(k string) string {
		if n := document.MapValue(v.Transform, k); n != nil {
			return n.Value
		}
		return ""
	}
	t := &transformTrace{Name: v.Name, Match: get("match"), Replace: get("replace")}
	t.From = snap.Subst().Resolve(get("from"), subst.Scope{})
	re, err := regexp.Compile(t.Match)
	if err != nil {
		t.Err = err
		return t
	}
	t.Matched = re.MatchString(t.From)
	t.Result = re.ReplaceAllString(t.From, t.Replace)
	return t
}

func (t *transformTrace) markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "`${{vars.%s}}` = `%s`\n\n", t.Name, t.Result)
	if t.Err != nil {
		fmt.Fprintf(&b, "`match` is not a valid regular expression: %v", t.Err)
		return b.String()
	}
	matched := "matches"
	if !t.Matched {
		matched = "no match"
	}
	b.WriteString(aligned([][]string{
		{"from", t.From, ""},
		{"match", t.Match, matched},
		{"replace", t.Replace, "→ " + t.Result},
	}))
	if !t.Matched {
		b.WriteString("The pattern does not match, so the variable is `from` unchanged.")
	}
	return b.String()
}

// updateTrace explains how the updater turns an upstream tag or release
// into a package version, following Chainguard's updater: tag filters,
// strip-prefix/suffix, ignore-regex-patterns, version-separator,
// pre-release skipping, then each version-transform in order.
type updateTrace struct {
	Monitor string
	// Input is the upstream tag or release version the trace starts from,
	// and Source says where it came from.
	Input, Source string
	// Actual is true when Input is the real upstream tag (from a
	// git-checkout of the monitored repository), not a reconstruction.
	Actual  bool
	Steps   []traceStep
	Result  string
	Skipped string // set when a filter drops the tag
	Version string // package.version
	// BuildTag is the tag the build checks out when a tag filter excludes
	// it, meaning the monitor watches a different tag series.
	BuildTag string
	// KeyNode is where to report a mismatch.
	KeyNode  *yaml.Node
	filtered bool
}

func (u *updateTrace) Matches() bool { return u.Skipped == "" && u.Result == u.Version && !u.Guess() }

// Guess reports a reconstruction the trace cannot vouch for: some
// version-transform did not match the reconstructed input, so the result
// equals package.version only because nothing was changed.
func (u *updateTrace) Guess() bool {
	if u.Actual {
		return false
	}
	for _, s := range u.Steps {
		if strings.HasPrefix(s.Label, "version-transform") && strings.HasSuffix(s.Note, "no match") {
			return true
		}
	}
	return false
}

type monitorSpec struct {
	name                                 string
	node                                 *yaml.Node
	stripPrefix, stripSuffix             string
	filter, filterPrefix, filterContains string
	identifier                           string
}

// readMonitor reads the update monitor, resolving ${{...}} in its fields
// as melange does when it parses the configuration.
func readMonitor(update *yaml.Node, resolve func(string) string) *monitorSpec {
	get := func(n *yaml.Node, k string) string {
		if v := document.MapValue(n, k); v != nil {
			return resolve(v.Value)
		}
		return ""
	}
	for _, name := range []string{"github", "git", "release-monitor"} {
		k, n := document.MapEntry(update, name)
		if k == nil {
			continue
		}
		m := &monitorSpec{name: name, node: k, stripPrefix: get(n, "strip-prefix"), stripSuffix: get(n, "strip-suffix"), identifier: get(n, "identifier")}
		switch name {
		case "github":
			m.filter = get(n, "tag-filter")
			m.filterPrefix, m.filterContains = get(n, "tag-filter-prefix"), get(n, "tag-filter-contains")
		case "git":
			m.filterPrefix, m.filterContains = get(n, "tag-filter-prefix"), get(n, "tag-filter-contains")
		case "release-monitor":
			m.filterPrefix, m.filterContains = get(n, "version-filter-prefix"), get(n, "version-filter-contains")
		}
		return m
	}
	return nil
}

type versionTransform struct{ match, replace string }

func readTransforms(update *yaml.Node, resolve func(string) string) []versionTransform {
	var out []versionTransform
	for _, t := range document.Items(document.MapValue(update, "version-transform")) {
		vt := versionTransform{}
		if m := document.MapValue(t, "match"); m != nil {
			vt.match = resolve(m.Value)
		}
		if r := document.MapValue(t, "replace"); r != nil {
			vt.replace = resolve(r.Value)
		}
		out = append(out, vt)
	}
	return out
}

// traceUpdate explains the update block, or returns nil if there is none.
func traceUpdate(snap *Snapshot) *updateTrace {
	d := snap.Doc
	update := document.MapValue(d.Root, "update")
	if update == nil {
		return nil
	}
	resolve := func(v string) string { return snap.Subst().Resolve(v, subst.Scope{}) }
	mon := readMonitor(update, resolve)
	if mon == nil {
		return nil
	}
	version := ""
	if v := document.MapValue(document.MapValue(d.Root, "package"), "version"); v != nil {
		version = v.Value
	}
	u := &updateTrace{Monitor: mon.name, Version: version, KeyNode: mon.node}
	transforms := readTransforms(update, resolve)
	sep := ""
	if s := document.MapValue(update, "version-separator"); s != nil {
		sep = s.Value
	}

	reconstructed := func() (string, string) {
		src := "a tag reconstructed from package.version"
		if mon.name == "release-monitor" {
			src = "a release-monitoring.org version, reconstructed from package.version"
		}
		return reconstructTag(version, mon, sep, transforms), src
	}
	if tag, src, ok := upstreamTag(snap, mon); ok {
		t := &updateTrace{Monitor: mon.name, Version: version, KeyNode: mon.node, Input: tag, Source: src, Actual: true}
		t.run(update, mon, sep, transforms, resolve)
		if !t.filtered {
			return t
		}
		// A tag filter excludes the tag the build uses: the monitor watches
		// another tag series, so trace the tag it would see instead.
		u.BuildTag = tag
	}
	u.Input, u.Source = reconstructed()
	u.run(update, mon, sep, transforms, resolve)
	return u
}

// run applies the updater's stages to u.Input.
func (u *updateTrace) run(update *yaml.Node, mon *monitorSpec, sep string, transforms []versionTransform, resolve func(string) string) {
	v := u.Input
	step := func(label, value, note string) { u.Steps = append(u.Steps, traceStep{label, value, note}) }
	filter := func(label, want string, ok bool) bool {
		if want == "" {
			return true
		}
		if !ok {
			u.Skipped = fmt.Sprintf("%s %q does not match %q, so the updater ignores this tag", label, want, v)
			u.filtered = true
			step(label+" "+want, v, "skipped")
			return false
		}
		step(label+" "+want, v, "passes")
		return true
	}
	if !filter("tag-filter", mon.filter, strings.HasPrefix(v, mon.filter)) ||
		!filter(filterLabel(mon, "prefix"), mon.filterPrefix, strings.HasPrefix(v, mon.filterPrefix)) ||
		!filter(filterLabel(mon, "contains"), mon.filterContains, strings.Contains(v, mon.filterContains)) {
		return
	}
	if mon.stripPrefix != "" {
		v = strings.TrimPrefix(v, mon.stripPrefix)
		step("strip-prefix "+mon.stripPrefix, v, "")
	}
	if mon.stripSuffix != "" {
		v = strings.TrimSuffix(v, mon.stripSuffix)
		step("strip-suffix "+mon.stripSuffix, v, "")
	}
	for _, p := range document.Items(document.MapValue(update, "ignore-regex-patterns")) {
		if re, err := regexp.Compile(resolve(p.Value)); err == nil && re.MatchString(v) {
			u.Skipped = fmt.Sprintf("ignore-regex-patterns %q matches %q, so the updater ignores it", p.Value, v)
			step("ignore "+p.Value, v, "skipped")
			return
		}
	}
	if sep != "" {
		v = strings.ReplaceAll(v, sep, ".")
		step(fmt.Sprintf("version-separator %q → \".\"", sep), v, "")
	}
	prerelease := document.MapValue(update, "enable-prerelease-tags")
	if prerelease == nil || prerelease.Value != "true" {
		for _, w := range []string{"alpha", "beta", "rc", "pre"} {
			if strings.Contains(strings.ToLower(v), w) {
				u.Skipped = fmt.Sprintf("%q looks like a pre-release (%q) and enable-prerelease-tags is not set, so the updater skips it", v, w)
				step("pre-release check", v, "skipped")
				return
			}
		}
	}
	for i, t := range transforms {
		re, err := regexp.Compile(t.match)
		if err != nil {
			step(fmt.Sprintf("version-transform[%d]", i), v, "invalid regex")
			continue
		}
		note := fmt.Sprintf("s/%s/%s/", t.match, t.replace)
		if !re.MatchString(v) {
			note += "  no match"
		}
		v = re.ReplaceAllString(v, t.replace)
		step(fmt.Sprintf("version-transform[%d]", i), v, note)
	}
	u.Result = v
}

func filterLabel(m *monitorSpec, kind string) string {
	if m.name == "release-monitor" {
		return "version-filter-" + kind
	}
	return "tag-filter-" + kind
}

// upstreamTag finds the upstream tag the build checks out: the resolved
// tag: of a git-checkout of the monitored repository.
func upstreamTag(snap *Snapshot, mon *monitorSpec) (tag, source string, ok bool) {
	if mon.name == "release-monitor" {
		return "", "", false
	}
	for _, s := range snap.Model.Steps {
		if s.Uses == nil || s.Uses.Value != "git-checkout" || s.Subpackage != nil || s.Test {
			continue
		}
		t := document.MapValue(s.With, "tag")
		repo := document.MapValue(s.With, "repository")
		if t == nil || repo == nil {
			continue
		}
		if mon.name == "github" && mon.identifier != "" && !sameRepo(repo.Value, mon.identifier) {
			continue
		}
		resolved := snap.Subst().Resolve(t.Value, subst.Scope{Subpackage: s.Subpackage, Inputs: stepInputs(snap, s)})
		if strings.Contains(resolved, "${{") {
			continue
		}
		return resolved, fmt.Sprintf("the git-checkout tag (line %d)", t.Line), true
	}
	return "", "", false
}

// sameRepo reports whether a repository URL is the GitHub identifier.
func sameRepo(url, identifier string) bool {
	u := strings.TrimSuffix(strings.TrimSuffix(strings.ToLower(url), "/"), ".git")
	return strings.HasSuffix(u, "github.com/"+strings.ToLower(identifier))
}

// reconstructTag guesses the upstream tag for version by undoing the
// stages that can be undone: literal version-transforms, the separator,
// and stripped prefixes and suffixes.
func reconstructTag(version string, mon *monitorSpec, sep string, transforms []versionTransform) string {
	v := version
	for i := len(transforms) - 1; i >= 0; i-- {
		if in, ok := invertTransform(transforms[i].match, transforms[i].replace, v); ok {
			v = in
		}
	}
	if sep != "" {
		v = strings.ReplaceAll(v, ".", sep)
	}
	prefix := mon.stripPrefix
	if prefix == "" {
		prefix = mon.filterPrefix
		if prefix == "" {
			prefix = mon.filter
		}
	}
	if !strings.HasPrefix(v, prefix) {
		v = prefix + v
	}
	return v + mon.stripSuffix
}

func (u *updateTrace) markdown() string {
	var b strings.Builder
	switch {
	case u.Guess():
		fmt.Fprintf(&b, "**Update check (%s):** the upstream version could not be worked out from `package.version` `%s`\n\n", u.Monitor, u.Version)
	case u.Skipped != "":
		fmt.Fprintf(&b, "**Update check (%s):** `%s` is skipped\n\n", u.Monitor, u.Input)
	case u.Matches():
		fmt.Fprintf(&b, "**Update check (%s):** `%s` → `%s` ✓ matches `package.version`\n\n", u.Monitor, u.Input, u.Result)
	default:
		fmt.Fprintf(&b, "**Update check (%s):** `%s` → `%s`; `package.version` is `%s`\n\n", u.Monitor, u.Input, u.Result, u.Version)
	}
	if u.BuildTag != "" {
		fmt.Fprintf(&b, "The build checks out `%s`, which the tag filter excludes, so the monitor watches a different tag series. ", u.BuildTag)
	}
	fmt.Fprintf(&b, "Starting from %s:\n\n", u.Source)
	rows := [][]string{{"upstream", u.Input, ""}}
	for _, s := range u.Steps {
		rows = append(rows, []string{s.Label, "→ " + s.Value, s.Note})
	}
	b.WriteString(aligned(rows))
	switch {
	case u.Skipped != "":
		b.WriteString("\n" + u.Skipped + ".")
	case !u.Matches() && u.Actual:
		b.WriteString("\nThe result differs from `package.version` as text. The updater compares versions semantically (for example `_p` patch levels and underscores), and the monitor may watch different tags than the build checks out, so this is not necessarily wrong.")
	case u.Guess():
		b.WriteString("\nA version-transform does not match this guess, so the trace only shows the settings, not a real upstream value.")
	case !u.Matches():
		b.WriteString("\nThe upstream tag could not be reconstructed exactly; the trace shows the stages applied to the guess.")
	}
	b.WriteString("\n\n_Stages follow Chainguard's updater: tag filters, strip-prefix/suffix, ignore-regex-patterns, version-separator, pre-release skipping, version-transform._")
	return tidyBlankLines(b.String())
}

// updateLens is the one-line summary for a code lens on update:.
func (u *updateTrace) lens() string {
	switch {
	case u.Guess():
		return "update: upstream version unknown; hover for the settings trace"
	case u.Skipped != "":
		return fmt.Sprintf("update: upstream %s is skipped by the update settings", u.Input)
	case u.Matches():
		return fmt.Sprintf("update: upstream %s → %s ✓", u.Input, u.Result)
	}
	return fmt.Sprintf("update: upstream %s → %s (package.version is %s)", u.Input, u.Result, u.Version)
}

// aligned renders rows as a fenced block with padded columns, which looks
// like a table in every editor's hover and in plain text.
func aligned(rows [][]string) string {
	widths := map[int]int{}
	for _, r := range rows {
		for i, c := range r {
			if n := len([]rune(c)); n > widths[i] {
				widths[i] = n
			}
		}
	}
	var b strings.Builder
	b.WriteString("```text\n")
	for _, r := range rows {
		var line strings.Builder
		for i, c := range r {
			line.WriteString(c)
			if i < len(r)-1 && widths[i+1] > 0 {
				line.WriteString(strings.Repeat(" ", widths[i]-len([]rune(c))+2))
			}
		}
		b.WriteString(strings.TrimRight(line.String(), " ") + "\n")
	}
	b.WriteString("```\n\n")
	return b.String()
}

var blankRuns = regexp.MustCompile(`\n{3,}`)

func tidyBlankLines(s string) string { return blankRuns.ReplaceAllString(s, "\n\n") }
