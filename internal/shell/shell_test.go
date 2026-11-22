package shell

import (
	"os"
	"os/exec"
	"path/filepath"
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

func TestBinaryPathIsQuotedPerShell(t *testing.T) {
	const bin = `/opt/my tools/it's/jmp`
	want := map[string]string{
		"bash":       `'/opt/my tools/it'\''s/jmp' add`,
		"zsh":        `'/opt/my tools/it'\''s/jmp' add`,
		"fish":       `'/opt/my tools/it\'s/jmp' add`,
		"powershell": `& '/opt/my tools/it''s/jmp' add`,
	}
	for sh, snippet := range want {
		script, err := InitScript(sh, bin)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(script, snippet) {
			t.Errorf("%s: binary path not quoted as %s", sh, snippet)
		}
		if strings.Contains(script, "{{") {
			t.Errorf("%s: unexpanded template placeholder", sh)
		}
	}
}

// bashHarness sources the generated bash integration with a fake jmp that
// logs its arguments, then runs body. It returns the log and bash's output.
func bashHarness(t *testing.T, body string) (calls, output string) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	dir := t.TempDir()
	// A path with a space and a quote proves the quoting end to end.
	binDir := filepath.Join(dir, "my tools", "it's")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(dir, "calls.log")
	fake := filepath.Join(binDir, "jmp")
	fakeBody := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + logFile + "'\n" +
		"case \"$1\" in\n" +
		"  jump) if [ -n \"$FAKE_JUMP_RC\" ]; then echo 'jmp: boom' >&2; exit $FAKE_JUMP_RC; fi; echo \"$FAKE_JUMP_TARGET\" ;;\n" +
		"  complete) printf 'my dir\\nplain\\n' ;;\n" +
		"esac\n"
	if err := os.WriteFile(fake, []byte(fakeBody), 0o755); err != nil {
		t.Fatal(err)
	}
	script, err := InitScript("bash", fake)
	if err != nil {
		t.Fatal(err)
	}
	scriptFile := filepath.Join(dir, "init.bash")
	if err := os.WriteFile(scriptFile, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(bash, "-n", scriptFile).CombinedOutput(); err != nil {
		t.Fatalf("generated bash script has a syntax error: %v\n%s", err, out)
	}
	cmd := exec.Command(bash, "--noprofile", "--norc", "-c", "source '"+scriptFile+"'\n"+body)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "T="+dir)
	out, _ := cmd.CombinedOutput()
	log, _ := os.ReadFile(logFile)
	return string(log), string(out)
}

// Regression: bash used to add weight on every prompt, not every visit.
func TestBashHookRecordsOnlyOnDirectoryChange(t *testing.T) {
	calls, out := bashHarness(t, `
mkdir -p "$T/a" "$T/b"
cd "$T/a"; _jmp_chpwd; _jmp_chpwd; _jmp_chpwd
cd "$T/b"; _jmp_chpwd; _jmp_chpwd
`)
	adds := 0
	for _, line := range strings.Split(calls, "\n") {
		if strings.HasPrefix(line, "add -- ") {
			adds++
		}
	}
	if adds != 2 {
		t.Fatalf("5 prompts in 2 directories recorded %d visits, want 2\ncalls:\n%s\noutput:\n%s", adds, calls, out)
	}
}

func TestBashJ(t *testing.T) {
	t.Run("jumps to the reported directory", func(t *testing.T) {
		_, out := bashHarness(t, `
mkdir -p "$T/target dir"
export FAKE_JUMP_TARGET="$T/target dir"
j foo && pwd`)
		if !strings.Contains(out, "target dir") {
			t.Fatalf("did not cd: %s", out)
		}
	})
	t.Run("keywords can be escaped with --", func(t *testing.T) {
		calls, _ := bashHarness(t, `
mkdir -p "$T/x"; export FAKE_JUMP_TARGET="$T/x"
JMP_BACK="$T"
j -- back`)
		if !strings.Contains(calls, "-- back") {
			t.Fatalf("`j -- back` was not passed to jmp as a query:\n%s", calls)
		}
	})
	t.Run("a real error is shown, not disguised as no match", func(t *testing.T) {
		_, out := bashHarness(t, `
export FAKE_JUMP_RC=1
j nothing-here; echo "rc=$?"`)
		if !strings.Contains(out, "jmp: boom") || strings.Contains(out, "no match") {
			t.Fatalf("error hidden: %s", out)
		}
	})
	t.Run("no match is reported as such", func(t *testing.T) {
		_, out := bashHarness(t, `
export FAKE_JUMP_RC=3
j nothing-here; echo "rc=$?"`)
		if !strings.Contains(out, "no match for: nothing-here") || !strings.Contains(out, "rc=1") {
			t.Fatalf("unexpected output: %s", out)
		}
	})
	t.Run("completion keeps names with spaces intact", func(t *testing.T) {
		_, out := bashHarness(t, `
COMP_WORDS=(j m); COMP_CWORD=1
_jmp_complete
printf '<%s>\n' "${COMPREPLY[@]}"`)
		if !strings.Contains(out, `<my\ dir>`) || !strings.Contains(out, "<plain>") {
			t.Fatalf("completion candidates: %s", out)
		}
	})
}
