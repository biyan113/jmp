package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/biyan113/jmp/internal/store"
)

// env is an isolated jmp "device": its own database and state directory.
type env struct {
	t   *testing.T
	dir string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	t.Setenv("JMP_LANG", "en")
	t.Setenv("JMP_DB", "")
	return &env{t: t, dir: t.TempDir()}
}

func (e *env) db() string { return filepath.Join(e.dir, "db.json") }

// run executes the CLI against this env and returns exit code and output.
func (e *env) run(args ...string) (code int, out, errOut string) {
	e.t.Helper()
	var o, eb bytes.Buffer
	stdout, stderr = &o, &eb
	defer func() { stdout, stderr = os.Stdout, os.Stderr }()
	code = run(append([]string{"--db", e.db()}, args...))
	return code, o.String(), eb.String()
}

func (e *env) mustRun(args ...string) string {
	e.t.Helper()
	code, out, errOut := e.run(args...)
	if code != 0 {
		e.t.Fatalf("jmp %s: exit %d\nstdout: %s\nstderr: %s", strings.Join(args, " "), code, out, errOut)
	}
	return out
}

func (e *env) weight(path string) float64 {
	e.t.Helper()
	s, err := store.Load(e.db())
	if err != nil {
		e.t.Fatal(err)
	}
	entry, ok := s.Entry(path)
	if !ok {
		return -1
	}
	return entry.Weight
}

func mkdir(t *testing.T, parts ...string) string {
	t.Helper()
	p := filepath.Join(parts...)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExitCodes(t *testing.T) {
	e := newEnv(t)
	dir := mkdir(t, e.dir, "proj")
	e.mustRun("add", dir)

	cases := []struct {
		name string
		args []string
		want int
	}{
		{"ok", []string{"jump", "proj"}, exitOK},
		{"unknown command", []string{"nope"}, exitUsage},
		{"unknown flag", []string{"list", "--bogus"}, exitUsage},
		{"missing argument", []string{"add"}, exitUsage},
		{"unexpected argument", []string{"stats", "extra"}, exitUsage},
		{"bad flag value", []string{"add", dir, "-w", "-5"}, exitUsage},
		{"bad color", []string{"config", "color", "pink"}, exitUsage},
		{"no match", []string{"jump", "zzzzqqqq"}, exitNotFound},
		{"remove unknown path", []string{"remove", "/no/such"}, exitNotFound},
		{"remove unknown alias", []string{"alias", "remove", "nope"}, exitNotFound},
		{"add non-directory", []string{"add", "/no/such/dir"}, exitError},
		{"sync unconfigured", []string{"sync"}, exitError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, out, errOut := e.run(c.args...)
			if code != c.want {
				t.Fatalf("exit = %d, want %d\nstdout: %s\nstderr: %s", code, c.want, out, errOut)
			}
			if strings.Contains(out+errOut, "Usage:") {
				t.Errorf("error output dumps the full usage text:\n%s%s", out, errOut)
			}
			if code != 0 && errOut == "" {
				t.Error("failure produced no message on stderr")
			}
			if code == exitUsage && !strings.Contains(errOut, "--help") {
				t.Errorf("usage error does not point at --help: %s", errOut)
			}
		})
	}
}

func TestVersionFlag(t *testing.T) {
	e := newEnv(t)
	if out := e.mustRun("--version"); out != "jmp "+version+"\n" {
		t.Fatalf("--version printed %q", out)
	}
	if out := e.mustRun("version"); out != "jmp "+version+"\n" {
		t.Fatalf("version printed %q", out)
	}
}

func TestAddRejectsNonDirectoriesAndKeepsGoing(t *testing.T) {
	e := newEnv(t)
	good := mkdir(t, e.dir, "good")
	file := filepath.Join(e.dir, "file.txt")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := e.run("add", "/no/such/dir", file, good)
	if code != exitError {
		t.Fatalf("exit = %d, want %d", code, exitError)
	}
	if !strings.Contains(errOut, "/no/such/dir") || !strings.Contains(errOut, file) {
		t.Errorf("rejected paths not named: %s", errOut)
	}
	if e.weight(good) != 1 {
		t.Error("the valid path in the same call was not recorded")
	}
	if e.weight("/no/such/dir") != -1 || e.weight(file) != -1 {
		t.Error("a non-directory was recorded")
	}
}

func TestAddWeightSetsExactly(t *testing.T) {
	e := newEnv(t)
	dir := mkdir(t, e.dir, "d")
	e.mustRun("add", dir)
	e.mustRun("add", dir, "-w", "50")
	if w := e.weight(dir); w != 50 {
		t.Fatalf("weight = %.1f, want 50", w)
	}
}

// Regression: alias set and import used to reset an existing weight.
func TestAliasSetAndImportPreserveExistingWeight(t *testing.T) {
	e := newEnv(t)
	dir := mkdir(t, e.dir, "data")
	fresh := mkdir(t, e.dir, "fresh")
	e.mustRun("add", dir, "-w", "500")

	e.mustRun("alias", "set", "d", dir)
	if w := e.weight(dir); w != 500 {
		t.Fatalf("alias set changed weight to %.1f", w)
	}

	hist := filepath.Join(e.dir, "hist.txt")
	content := "cd " + dir + "\ncd " + dir + "\ncd " + fresh + "\ncd /no/such\n"
	if err := os.WriteFile(hist, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	out := e.mustRun("import", hist)
	if w := e.weight(dir); w != 500 {
		t.Fatalf("import changed weight to %.1f", w)
	}
	if !strings.Contains(out, "imported 1 new paths (1 already known)") {
		t.Errorf("import summary = %q", out)
	}
	if e.weight(fresh) != 1 {
		t.Error("new path not imported")
	}
}

func TestImportFromStdin(t *testing.T) {
	e := newEnv(t)
	dir := mkdir(t, e.dir, "piped")
	stdin = strings.NewReader(dir + "\n")
	defer func() { stdin = os.Stdin }()
	e.mustRun("import", "-")
	if e.weight(dir) != 1 {
		t.Fatal("path from stdin not imported")
	}
}

func TestAliasNameIsValidated(t *testing.T) {
	e := newEnv(t)
	dir := mkdir(t, e.dir, "d")
	for _, bad := range []string{"", "a b", "@"} {
		if code, _, _ := e.run("alias", "set", bad, dir); code != exitUsage {
			t.Errorf("alias set %q: exit %d, want %d", bad, code, exitUsage)
		}
	}
	file := filepath.Join(e.dir, "f")
	_ = os.WriteFile(file, nil, 0o644)
	if code, _, _ := e.run("alias", "set", "f", file); code != exitError {
		t.Errorf("alias to a file: exit %d, want %d", code, exitError)
	}
}

// Regression: jumping used to delete every record whose directory was
// momentarily missing (e.g. an unmounted volume).
func TestJumpNeverPrunesMissingDirectories(t *testing.T) {
	e := newEnv(t)
	vol := mkdir(t, e.dir, "vol", "data")
	proj := mkdir(t, e.dir, "proj")
	e.mustRun("add", vol, "-w", "500")
	e.mustRun("add", proj)

	off := filepath.Join(e.dir, "vol.off")
	if err := os.Rename(filepath.Join(e.dir, "vol"), off); err != nil { // "unmount"
		t.Fatal(err)
	}
	if out := e.mustRun("jump", "proj"); strings.TrimSpace(out) != proj {
		t.Fatalf("jump printed %q", out)
	}
	// The missing directory is skipped as a target...
	if code, _, _ := e.run("jump", "data"); code != exitNotFound {
		t.Errorf("jump to unmounted dir: exit %d, want %d", code, exitNotFound)
	}
	// ...but its record and weight survive.
	if w := e.weight(vol); w != 500 {
		t.Fatalf("record for unmounted volume: weight %.1f, want 500", w)
	}
	if err := os.Rename(off, filepath.Join(e.dir, "vol")); err != nil { // "remount"
		t.Fatal(err)
	}
	if out := e.mustRun("jump", "data"); strings.TrimSpace(out) != vol {
		t.Fatalf("after remount jump printed %q", out)
	}
}

func TestCleanDryRunListsWithoutRemoving(t *testing.T) {
	e := newEnv(t)
	gone := mkdir(t, e.dir, "gone")
	e.mustRun("add", gone)
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	out := e.mustRun("clean", "--dry-run")
	if !strings.Contains(out, gone) {
		t.Errorf("dry run does not name the path:\n%s", out)
	}
	if e.weight(gone) == -1 {
		t.Fatal("dry run removed the record")
	}
	e.mustRun("clean")
	if e.weight(gone) != -1 {
		t.Fatal("clean did not remove the record")
	}
}

func TestListOutputModes(t *testing.T) {
	e := newEnv(t)
	a := mkdir(t, e.dir, "alpha")
	b := mkdir(t, e.dir, "beta")
	e.mustRun("add", a, b)

	if out := e.mustRun("list", "--paths", "alpha"); out != a+"\n" {
		t.Errorf("list --paths alpha = %q", out)
	}
	if out := e.mustRun("list", "-q", "beta", "-p"); out != b+"\n" {
		t.Errorf("list -q beta -p = %q", out)
	}
	table := e.mustRun("list")
	if !strings.HasPrefix(table, "SCORE") || strings.Contains(table, "----") {
		t.Errorf("unexpected table:\n%s", table)
	}
	if out := e.mustRun("list", "--no-header"); strings.Contains(out, "SCORE") {
		t.Errorf("--no-header still prints the header:\n%s", out)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(e.mustRun("list", "--json")), &rows); err != nil || len(rows) != 2 {
		t.Errorf("list --json: %v, %d rows", err, len(rows))
	}
}

func TestJSONModeAlwaysEmitsJSON(t *testing.T) {
	e := newEnv(t)
	dir := mkdir(t, e.dir, "d")
	cmds := [][]string{
		{"add", dir},
		{"alias", "set", "d", dir},
		{"alias", "remove", "d"},
		{"alias", "remove", "d"}, // error path
		{"remove", "/no/such"},   // error path
		{"jump", "zzzzqqqq"},     // error path
		{"sync", "status"},
		{"sync"}, // error path
		{"clean", "--dry-run"},
		{"stats"},
		{"doctor"},
	}
	for _, c := range cmds {
		_, out, errOut := e.run(append(c, "--json")...)
		var v any
		if err := json.Unmarshal([]byte(out), &v); err != nil {
			t.Errorf("jmp %s --json: stdout is not JSON (%v): %q (stderr %q)", strings.Join(c, " "), err, out, errOut)
		}
	}
}

func TestQuietSuppressesStatusButNotData(t *testing.T) {
	e := newEnv(t)
	dir := mkdir(t, e.dir, "d")
	e.mustRun("add", dir)
	if out := e.mustRun("--quiet", "alias", "set", "d", dir); out != "" {
		t.Errorf("--quiet printed %q", out)
	}
	if out := e.mustRun("--quiet", "list", "-p"); out != dir+"\n" {
		t.Errorf("--quiet hid requested data: %q", out)
	}
}

func TestJMPDBEnvSelectsDatabase(t *testing.T) {
	e := newEnv(t)
	dir := mkdir(t, e.dir, "d")
	custom := filepath.Join(e.dir, "custom", "db.json")
	t.Setenv("JMP_DB", custom)

	var o bytes.Buffer
	stdout, stderr = &o, &o
	defer func() { stdout, stderr = os.Stdout, os.Stderr }()
	if code := run([]string{"add", dir}); code != 0 {
		t.Fatalf("exit %d: %s", code, o.String())
	}
	if _, err := os.Stat(custom); err != nil {
		t.Fatalf("JMP_DB was ignored: %v", err)
	}
}

func TestMessagesFollowLocale(t *testing.T) {
	e := newEnv(t)
	t.Setenv("JMP_LANG", "zh_CN.UTF-8")
	if _, _, errOut := e.run("jump", "zzzzqqqq"); !strings.Contains(errOut, "没有匹配") {
		t.Errorf("zh message missing: %q", errOut)
	}
	t.Setenv("JMP_LANG", "en")
	if _, _, errOut := e.run("jump", "zzzzqqqq"); !strings.Contains(errOut, "no match for") {
		t.Errorf("en message missing: %q", errOut)
	}
}

func TestCorruptDatabaseRecoversWithWarning(t *testing.T) {
	e := newEnv(t)
	dir := mkdir(t, e.dir, "d")
	e.mustRun("add", dir, "-w", "7")
	e.mustRun("add", dir, "-w", "7") // second write snapshots the backup
	if err := os.WriteFile(e.db(), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := e.run("add", dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(errOut, "corrupt") {
		t.Errorf("no warning about the recovery: %q", errOut)
	}
	if w := e.weight(dir); w != 8 {
		t.Fatalf("weight after recovery = %.1f, want 8 (7 from backup + 1)", w)
	}
}

// ---- sync ----

func TestParseRemote(t *testing.T) {
	cases := []struct{ raw, host, path string }{
		{"user@host:~/.local/share/jmp/db.json", "user@host", "~/.local/share/jmp/db.json"},
		{"host:/abs/db.json", "host", "/abs/db.json"},
		{"[::1]:/abs/db.json", "[::1]", "/abs/db.json"},
		{"me@[fe80::1]:db.json", "me@[fe80::1]", "db.json"},
		{"/mnt/share/db.json", "", "/mnt/share/db.json"},
		{"./rel/a:b.json", "", "./rel/a:b.json"},
		{`C:\Users\me\db.json`, "", `C:\Users\me\db.json`},
		{"C:/Users/me/db.json", "", "C:/Users/me/db.json"},
	}
	for _, c := range cases {
		r := parseRemote(c.raw)
		if r.host != c.host || r.path != c.path {
			t.Errorf("parseRemote(%q) = {%q %q}, want {%q %q}", c.raw, r.host, r.path, c.host, c.path)
		}
	}
	if got := remoteShellQuote("~/a b/it's"); got != `~/'a b/it'\''s'` {
		t.Errorf("remoteShellQuote = %s", got)
	}
}

func TestSyncSetValidation(t *testing.T) {
	e := newEnv(t)
	for _, args := range [][]string{
		{"sync", "set", "h:db.json", "30"},
		{"sync", "set", "h:db.json", "abc"},
		{"sync", "set", "h:db.json", "--interval", "5"},
		{"sync", "set", "--", "-oProxyCommand=evil"},
	} {
		if code, _, _ := e.run(args...); code != exitUsage {
			t.Errorf("jmp %s: exit %d, want %d", strings.Join(args, " "), code, exitUsage)
		}
	}
	e.mustRun("sync", "set", "h:db.json", "--interval", "600")
	if out := e.mustRun("sync", "status"); !strings.Contains(out, "600s") {
		t.Errorf("status = %s", out)
	}
	e.mustRun("sync", "unset")
	if out := e.mustRun("sync", "status"); !strings.Contains(out, "not configured") {
		t.Errorf("status after unset = %s", out)
	}
}

func TestSyncBetweenTwoDevices(t *testing.T) {
	a, b := newEnv(t), newEnv(t)
	shared := filepath.Join(t.TempDir(), "remote", "db.json")
	one := mkdir(t, t.TempDir(), "one")
	two := mkdir(t, t.TempDir(), "two")
	a.mustRun("sync", "set", shared)
	b.mustRun("sync", "set", shared)

	a.mustRun("add", one)
	a.mustRun("alias", "set", "one", one)
	if out := a.mustRun("sync"); !strings.Contains(out, "created") {
		t.Errorf("first sync should seed the remote: %s", out)
	}
	b.mustRun("add", two)
	if out := b.mustRun("sync", "-v"); !strings.Contains(out, "pull + "+one) || !strings.Contains(out, "push + "+two) {
		t.Errorf("verbose sync does not itemise changes:\n%s", out)
	}
	a.mustRun("sync")
	if a.weight(two) == -1 || b.weight(one) == -1 {
		t.Fatal("devices did not converge")
	}
	if out := a.mustRun("sync"); !strings.Contains(out, "+0 ~0 -0") {
		t.Errorf("a no-op sync reports changes: %s", out)
	}

	// Removals must propagate instead of being resurrected.
	a.mustRun("remove", two)
	a.mustRun("alias", "remove", "one")
	a.mustRun("sync")
	b.mustRun("sync")
	if b.weight(two) != -1 {
		t.Error("removed path survived on the other device")
	}
	if out := b.mustRun("alias"); strings.Contains(out, "@one") {
		t.Error("removed alias survived on the other device")
	}
	a.mustRun("sync")
	if a.weight(two) != -1 {
		t.Error("removed path came back after a round trip")
	}
}

func TestSyncDryRunChangesNothing(t *testing.T) {
	a, b := newEnv(t), newEnv(t)
	shared := filepath.Join(t.TempDir(), "db.json")
	one := mkdir(t, t.TempDir(), "one")
	two := mkdir(t, t.TempDir(), "two")
	a.mustRun("sync", "set", shared)
	b.mustRun("sync", "set", shared)
	a.mustRun("add", one)
	a.mustRun("sync")
	b.mustRun("add", two)

	before, _ := os.ReadFile(shared)
	out := b.mustRun("sync", "--dry-run", "-v")
	if !strings.Contains(out, "dry run") || !strings.Contains(out, "pull + "+one) {
		t.Errorf("dry run output:\n%s", out)
	}
	after, _ := os.ReadFile(shared)
	if !bytes.Equal(before, after) {
		t.Error("dry run modified the remote")
	}
	if b.weight(one) != -1 {
		t.Error("dry run modified the local database")
	}
}

// The central safety property: a failed pull must never be followed by a push.
func TestSyncNeverPushesAfterFailedPull(t *testing.T) {
	e := newEnv(t)
	dir := mkdir(t, e.dir, "d")
	e.mustRun("add", dir)

	// Unreadable remote: the path is a directory, so reading it fails with
	// something other than "does not exist".
	remoteDir := mkdir(t, t.TempDir(), "db.json")
	e.mustRun("sync", "set", remoteDir)
	code, _, errOut := e.run("sync")
	if code != exitSync {
		t.Fatalf("exit = %d, want %d (%s)", code, exitSync, errOut)
	}
	if !strings.Contains(errOut, "left untouched") {
		t.Errorf("error does not say the remote is safe: %s", errOut)
	}
	if info, err := os.Stat(remoteDir); err != nil || !info.IsDir() {
		t.Fatal("remote was overwritten after a failed pull")
	}

	// Corrupt remote: also abort, and leave it alone.
	corrupt := filepath.Join(t.TempDir(), "db.json")
	if err := os.WriteFile(corrupt, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.mustRun("sync", "set", corrupt)
	if code, _, _ := e.run("sync"); code != exitSync {
		t.Fatalf("corrupt remote: exit = %d, want %d", code, exitSync)
	}
	if data, _ := os.ReadFile(corrupt); string(data) != "{not json" {
		t.Fatal("corrupt remote was overwritten")
	}
	// The failure is visible in status.
	if out := e.mustRun("sync", "status"); !strings.Contains(out, "last error:") {
		t.Errorf("status hides the failure:\n%s", out)
	}
}

func TestSyncAutoBacksOffAfterFailure(t *testing.T) {
	e := newEnv(t)
	remoteDir := mkdir(t, t.TempDir(), "db.json") // always fails
	e.mustRun("sync", "set", remoteDir)

	e.mustRun("sync", "auto", "--foreground")
	first := loadStateAt(t, e)
	if first.LastAttempt.IsZero() || first.LastError == "" {
		t.Fatalf("failed attempt not recorded: %+v", first)
	}
	// Mark the attempt so a second run would be detectable.
	e.mustRun("sync", "auto", "--foreground")
	second := loadStateAt(t, e)
	if !second.LastAttempt.Equal(first.LastAttempt) {
		t.Fatal("auto sync retried immediately after a failure instead of waiting for the interval")
	}
}

func loadStateAt(t *testing.T, e *env) syncState {
	t.Helper()
	var st syncState
	data, err := os.ReadFile(filepath.Join(e.dir, "sync_state.json"))
	if err != nil {
		t.Fatalf("sync state not stored next to the custom database: %v", err)
	}
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatal(err)
	}
	return st
}

// fakeRemoteTools puts stand-in scp/ssh scripts on PATH that operate on local
// files, so the real SSH code path (options, temp upload, remote rename) is
// exercised without a server. Every invocation is appended to the log.
func fakeRemoteTools(t *testing.T) (logFile string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	bin := t.TempDir()
	logFile = filepath.Join(bin, "calls.log")
	scp := `#!/bin/sh
echo "scp $*" >> "` + logFile + `"
while [ $# -gt 2 ]; do
  case "$1" in -o) shift 2 ;; *) shift ;; esac
done
if [ -n "$FAKE_REMOTE_DOWN" ]; then echo "ssh: connect to host fake port 22: Connection refused" >&2; exit 255; fi
src="${1#fake:}"; dst="${2#fake:}"
if [ ! -e "$src" ]; then echo "scp: $src: No such file or directory" >&2; exit 1; fi
cp "$src" "$dst"
`
	ssh := `#!/bin/sh
echo "ssh $*" >> "` + logFile + `"
while [ $# -gt 2 ]; do
  case "$1" in -o) shift 2 ;; *) shift ;; esac
done
if [ -n "$FAKE_REMOTE_DOWN" ]; then echo "ssh: connect to host fake port 22: Connection refused" >&2; exit 255; fi
sh -c "$2"
`
	for name, body := range map[string]string{"scp": scp, "ssh": ssh} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logFile
}

func TestSyncOverSSH(t *testing.T) {
	logFile := fakeRemoteTools(t)
	e := newEnv(t)
	dir := mkdir(t, e.dir, "d")
	e.mustRun("add", dir)
	remotePath := filepath.Join(t.TempDir(), "nested", "it's here", "db.json")
	e.mustRun("sync", "set", "fake:"+remotePath)

	// First sync: remote missing -> seed (creating the directory).
	if out := e.mustRun("sync"); !strings.Contains(out, "created") {
		t.Fatalf("seed sync: %s", out)
	}
	if _, err := store.Load(remotePath); err != nil {
		t.Fatalf("remote not written: %v", err)
	}
	logData, _ := os.ReadFile(logFile)
	log := string(logData)
	for _, want := range []string{"BatchMode=yes", "ConnectTimeout=10", "mkdir -p", ".tmp-", "mv -f"} {
		if !strings.Contains(log, want) {
			t.Errorf("remote calls lack %q:\n%s", want, log)
		}
	}
	leftovers, _ := filepath.Glob(remotePath + ".tmp-*")
	if len(leftovers) != 0 {
		t.Errorf("temporary upload left on the remote: %v", leftovers)
	}

	// Remote unreachable: abort with the sync exit code, push nothing.
	other := mkdir(t, e.dir, "other")
	e.mustRun("add", other)
	before, _ := os.ReadFile(remotePath)
	t.Setenv("FAKE_REMOTE_DOWN", "1")
	code, _, errOut := e.run("sync")
	if code != exitSync || !strings.Contains(errOut, "Connection refused") {
		t.Fatalf("unreachable remote: exit %d, stderr %q", code, errOut)
	}
	after, _ := os.ReadFile(remotePath)
	if !bytes.Equal(before, after) {
		t.Fatal("remote changed although the pull failed")
	}

	// Back online: the pending change goes through.
	t.Setenv("FAKE_REMOTE_DOWN", "")
	e.mustRun("sync")
	rs, err := store.Load(remotePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rs.Entry(other); !ok {
		t.Error("change not uploaded after the remote came back")
	}
}

func TestSyncRefusesToRunConcurrently(t *testing.T) {
	e := newEnv(t)
	e.mustRun("sync", "set", filepath.Join(t.TempDir(), "db.json"))
	release, err := store.AcquireLock(filepath.Join(e.dir, "sync.lock"), 0, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if code, _, errOut := e.run("sync"); code != exitSync || !strings.Contains(errOut, "already running") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
}
