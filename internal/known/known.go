// Package known holds facts about melange that it does not export: linter
// names, allowed capabilities, and documentation for built-in variables.
package known

//go:generate go run ../../hack/genlinters

// Linter is one of melange's package linters.
type Linter struct {
	Name    string
	Explain string
	// Default is "require", "warn", or "ignore".
	Default string
}

// LinterByName returns the named linter.
func LinterByName(name string) (Linter, bool) {
	for _, l := range Linters {
		if l.Name == name {
			return l, true
		}
	}
	return Linter{}, false
}

// SetcapCapabilities are the file capabilities melange accepts in setcap:.
var SetcapCapabilities = []string{"cap_net_bind_service", "cap_net_admin", "cap_net_raw", "cap_ipc_lock", "cap_sys_admin"}

// RunnerCapabilities are the Linux capabilities for needs.capabilities
// and capabilities.add/drop.
var RunnerCapabilities = []string{
	"CAP_AUDIT_CONTROL", "CAP_AUDIT_READ", "CAP_AUDIT_WRITE", "CAP_BLOCK_SUSPEND", "CAP_BPF",
	"CAP_CHECKPOINT_RESTORE", "CAP_CHOWN", "CAP_DAC_OVERRIDE", "CAP_DAC_READ_SEARCH", "CAP_FOWNER",
	"CAP_FSETID", "CAP_IPC_LOCK", "CAP_IPC_OWNER", "CAP_KILL", "CAP_LEASE", "CAP_LINUX_IMMUTABLE",
	"CAP_MAC_ADMIN", "CAP_MAC_OVERRIDE", "CAP_MKNOD", "CAP_NET_ADMIN", "CAP_NET_BIND_SERVICE",
	"CAP_NET_BROADCAST", "CAP_NET_RAW", "CAP_PERFMON", "CAP_SETFCAP", "CAP_SETGID", "CAP_SETPCAP",
	"CAP_SETUID", "CAP_SYSLOG", "CAP_SYS_ADMIN", "CAP_SYS_BOOT", "CAP_SYS_CHROOT", "CAP_SYS_MODULE",
	"CAP_SYS_NICE", "CAP_SYS_PACCT", "CAP_SYS_PTRACE", "CAP_SYS_RAWIO", "CAP_SYS_RESOURCE",
	"CAP_SYS_TIME", "CAP_SYS_TTY_CONFIG", "CAP_WAKE_ALARM",
}

// Scope says where a variable is defined.
type Scope int

const (
	// Anywhere a step's fields are compiled.
	ScopePackage Scope = iota
	// Only inside subpackages (and their tests).
	ScopeSubpackage
	// Only inside subpackages with range:.
	ScopeRange
	// Only inside pipeline definitions, or children of a step with with:.
	ScopeInputs
)

// Variable documents a built-in ${{...}} variable. Names ending in "."
// are prefixes, e.g. "vars.".
type Variable struct {
	Name  string
	Doc   string
	Scope Scope
}

// Variables are melange's substitutions, from pkg/config/vars.go and
// build.NewSubstitutionMap.
var Variables = []Variable{
	{"package.name", "The package name.", ScopePackage},
	{"package.version", "The package version.", ScopePackage},
	{"package.epoch", "The package epoch.", ScopePackage},
	{"package.full-version", "`version-rEPOCH`, e.g. `1.2.3-r0`.", ScopePackage},
	{"package.description", "The package description. Only substituted when written exactly `${{package.description}}`, and not in `runs:` or `with:`.", ScopePackage},
	{"package.srcdir", "The build workspace, `/home/build`.", ScopePackage},
	{"targets.outdir", "The directory holding every package's install tree, `/home/build/melange-out`.", ScopePackage},
	{"targets.destdir", "The main package's install tree, `/home/build/melange-out/<package>`.", ScopePackage},
	{"targets.contextdir", "The install tree of the package being built: the main package, or the subpackage inside `subpackages:`.", ScopePackage},
	{"targets.subpkgdir", "The current subpackage's install tree, `/home/build/melange-out/<subpackage>`. Only inside `subpackages:`.", ScopeSubpackage},
	{"targets.package.", "`targets.package.<name>` is the install tree of the named package or subpackage.", ScopePackage},
	{"context.name", "The name of the package being built: the main package, or the subpackage inside `subpackages:`.", ScopePackage},
	{"subpkg.name", "The current subpackage's name. Only inside `subpackages:`.", ScopeSubpackage},
	{"build.arch", "The build architecture in APK form, e.g. `x86_64`, `aarch64`.", ScopePackage},
	{"build.goarch", "The build architecture in Go form, e.g. `amd64`, `arm64`.", ScopePackage},
	{"host.triplet.gnu", "The GNU host triplet, e.g. `x86_64-pc-linux-gnu`.", ScopePackage},
	{"host.triplet.rust", "The Rust host triplet, e.g. `x86_64-unknown-linux-gnu`.", ScopePackage},
	{"cross.triplet.gnu.glibc", "The GNU glibc triplet.", ScopePackage},
	{"cross.triplet.gnu.musl", "The GNU musl triplet.", ScopePackage},
	{"cross.triplet.rust.glibc", "The Rust glibc triplet.", ScopePackage},
	{"cross.triplet.rust.musl", "The Rust musl triplet.", ScopePackage},
	{"vars.", "A variable from `vars:` or a `var-transforms:` `to:`.", ScopePackage},
	{"options.", "`options.<name>.enabled` is `true` when the build option is enabled.", ScopePackage},
	{"range.key", "The current `data:` item's key. Only in `range:` subpackages, written exactly `${{range.key}}`.", ScopeRange},
	{"range.value", "The current `data:` item's value. Only in `range:` subpackages, written exactly `${{range.value}}`.", ScopeRange},
	{"inputs.", "`inputs.<name>` is a pipeline input set with `with:` or defaulted.", ScopeInputs},
}

// VariableDoc returns the entry documenting name, matching prefixes.
func VariableDoc(name string) (Variable, bool) {
	for _, v := range Variables {
		if v.Name == name || (v.Name[len(v.Name)-1] == '.' && len(name) > len(v.Name) && name[:len(v.Name)] == v.Name) {
			return v, true
		}
	}
	return Variable{}, false
}
