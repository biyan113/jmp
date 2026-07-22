package shell

import (
	"strings"
	"testing"
)

// TestInitScriptSubstitutesBin ensures the binary path is interpolated into
// every generated script and that no fmt formatting artifacts leak through.
func TestInitScriptSubstitutesBin(t *testing.T) {
	const bin = "/usr/local/bin/jmp"
	cases := []struct {
		name  string
		shell string
	}{
		{"bash", "bash"},
		{"zsh", "zsh"},
		{"fish", "fish"},
		{"powershell", "powershell"},
		{"pwsh alias", "pwsh"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			script, err := InitScript(c.shell, bin)
			if err != nil {
				t.Fatalf("InitScript(%q) error: %v", c.shell, err)
			}
			if !strings.Contains(script, bin) {
				t.Errorf("script for %s does not contain bin path %q", c.shell, bin)
			}
			// Catch any Sprintf mismatch (extra/missing %s) that would render
			// as Go's "%!s(MISSING)" / "%!(EXTRA ...)" placeholders.
			for _, bad := range []string{"%!s(MISSING)", "%!d(MISSING)", "%!(EXTRA"} {
				if strings.Contains(script, bad) {
					t.Errorf("script for %s contains format error marker %q", c.shell, bad)
				}
			}
		})
	}
}

// TestInitScriptUnsupported rejects unknown shells.
func TestInitScriptUnsupported(t *testing.T) {
	if _, err := InitScript("tcsh", "jmp"); err == nil {
		t.Fatal("expected error for unsupported shell, got nil")
	}
}

// TestInitScriptHasCDBusin checks that the cd-fallback branch is present in
// every generated script, so a DB miss still lets `j` act like `cd`.
func TestInitScriptHasCDFallback(t *testing.T) {
	const bin = "jmp"
	markers := map[string]string{
		"bash":       `local _dir`,
		"zsh":        `local _dir`, // zsh mirrors the bash branch verbatim
		"fish":       `set _dir`,
		"powershell": `$dir = ($Query -join ' ')`,
	}
	for sh, marker := range markers {
		t.Run(sh, func(t *testing.T) {
			script, err := InitScript(sh, bin)
			if err != nil {
				t.Fatalf("InitScript(%q) error: %v", sh, err)
			}
			if !strings.Contains(script, marker) {
				t.Errorf("%s script missing cd-fallback marker %q", sh, marker)
			}
		})
	}
}
