package provider

import _ "embed"

// symlinkInstallScript is the embedded install script of the builtin
// symlink provider. Builtin scripts do not live under the data root's
// provider dir, so the executor serves them from here.
//
//go:embed builtin/symlink.sh
var symlinkInstallScript string

// builtinScripts maps builtin provider id and declared script path to the
// embedded script content. The git builtin is code, not a script, and is
// dispatched by the executor (see warmupGit/installGit in executor.go).
var builtinScripts = map[string]map[string]string{
	"symlink": {"scripts/install.sh": symlinkInstallScript},
}

// builtinScript returns the embedded script content for a builtin
// provider's declared script path, if one exists.
func builtinScript(id, script string) (string, bool) {
	content, ok := builtinScripts[id][script]
	return content, ok
}
