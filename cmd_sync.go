package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/biyan113/jmp/internal/store"
	"github.com/spf13/cobra"
)

const (
	defaultSyncInterval = 300
	minSyncInterval     = 60
	// remoteTimeout bounds every scp/ssh invocation so a dead network can
	// never leave a sync hanging.
	remoteTimeout = 60 * time.Second
	// syncLockStale is the age after which a sync lock left by a crashed
	// process is broken.
	syncLockStale = 5 * time.Minute
)

// sshOptions make every remote call non-interactive: without BatchMode a
// background sync could pop a password prompt into the user's terminal.
var sshOptions = []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=10"}

// SyncConfig stores remote sync settings.
type SyncConfig struct {
	Remote   string `json:"remote"`
	Interval int    `json:"interval"`
}

// syncState records the outcome of sync attempts. LastAttempt is what the
// automatic sync throttles on, so a failing remote is retried once per
// interval instead of once per prompt.
type syncState struct {
	LastSuccess time.Time `json:"last_success"`
	LastAttempt time.Time `json:"last_attempt"`
	LastError   string    `json:"last_error,omitempty"`
}

func syncConfigFile() string { return filepath.Join(stateDir(), "sync.json") }
func syncStateFile() string  { return filepath.Join(stateDir(), "sync_state.json") }
func syncLockFile() string   { return filepath.Join(stateDir(), "sync.lock") }

func loadSyncConfig() (*SyncConfig, error) {
	data, err := os.ReadFile(syncConfigFile())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg SyncConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", syncConfigFile(), err)
	}
	if cfg.Interval < minSyncInterval {
		cfg.Interval = defaultSyncInterval
	}
	return &cfg, nil
}

func requireSyncConfig() (*SyncConfig, error) {
	cfg, err := loadSyncConfig()
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, fmt.Errorf("%s", T("sync is not configured; run: jmp sync set <remote>", "同步未配置，运行: jmp sync set <remote>"))
	}
	return cfg, nil
}

func loadSyncState() syncState {
	var st syncState
	if data, err := os.ReadFile(syncStateFile()); err == nil {
		_ = json.Unmarshal(data, &st)
	}
	return st
}

func saveSyncState(st syncState) { _ = writeJSONFile(syncStateFile(), st) }

func recordSyncResult(err error) {
	st := loadSyncState()
	st.LastAttempt = time.Now()
	if err == nil {
		st.LastSuccess = st.LastAttempt
		st.LastError = ""
	} else {
		st.LastError = err.Error()
	}
	saveSyncState(st)
}

// ---- remote transport ----

// errRemoteMissing means the remote was reached and has no database yet. It
// is the only pull failure after which seeding the remote is safe.
var errRemoteMissing = errors.New("remote database does not exist")

// remote is a sync target: either host:path reached over scp/ssh, or a plain
// local path (a mounted share, a synced folder).
type remote struct {
	host string // empty for a local path
	path string
}

func parseRemote(raw string) remote {
	// [ipv6]:path, optionally user@[ipv6]:path
	if i := strings.Index(raw, "]:"); i > 0 && strings.Contains(raw[:i], "[") {
		return remote{host: raw[:i+1], path: raw[i+2:]}
	}
	i := strings.Index(raw, ":")
	if i <= 0 || strings.ContainsAny(raw[:i], `/\`) {
		return remote{path: raw}
	}
	if i == 1 && len(raw) > 2 && (raw[2] == '\\' || raw[2] == '/') { // C:\path
		return remote{path: raw}
	}
	return remote{host: raw[:i], path: raw[i+1:]}
}

func (r remote) String() string {
	if r.host == "" {
		return r.path
	}
	return r.host + ":" + r.path
}

// sshHost is the host as ssh (not scp) wants it: without IPv6 brackets.
func (r remote) sshHost() string {
	return strings.NewReplacer("[", "", "]", "").Replace(r.host)
}

// remoteShellQuote quotes a path for the remote login shell while keeping a
// leading ~/ expandable.
func remoteShellQuote(p string) string {
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
	if p == "~" {
		return "~"
	}
	if strings.HasPrefix(p, "~/") {
		return "~/" + q(p[2:])
	}
	return q(p)
}

// runRemote runs scp/ssh with a timeout and a C locale (so error text can be
// recognised) and returns the combined output.
func runRemote(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), remoteTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, name, args...)
	c.Env = append(os.Environ(), "LC_ALL=C")
	out, err := c.CombinedOutput()
	msg := strings.TrimSpace(string(out))
	if ctx.Err() == context.DeadlineExceeded {
		return msg, fmt.Errorf("%s timed out after %s", name, remoteTimeout)
	}
	if err != nil {
		if msg == "" {
			msg = err.Error()
		}
		return msg, fmt.Errorf("%s: %s", name, msg)
	}
	return msg, nil
}

// fetch downloads the remote database to dst.
func (r remote) fetch(dst string) error {
	if r.host == "" {
		src, err := expandPath(r.path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(src)
		if os.IsNotExist(err) {
			return errRemoteMissing
		}
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o600)
	}
	args := append([]string{"-q"}, sshOptions...)
	out, err := runRemote("scp", append(args, r.String(), dst)...)
	if err != nil {
		if strings.Contains(strings.ToLower(out), "no such file") {
			return errRemoteMissing
		}
		return err
	}
	return nil
}

// push uploads src to the remote atomically: it is written under a temporary
// name and renamed into place, so another device never reads a half-written
// database. seed is set on the very first push, when the directory may not
// exist yet.
func (r remote) push(src string, seed bool) error {
	host, _ := os.Hostname()
	suffix := fmt.Sprintf(".tmp-%s-%d", strings.NewReplacer("/", "_", " ", "_").Replace(host), os.Getpid())

	if r.host == "" {
		dst, err := expandPath(r.path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		tmp := dst + suffix
		if err := os.WriteFile(tmp, data, 0o644); err != nil {
			return err
		}
		if err := os.Rename(tmp, dst); err != nil {
			os.Remove(tmp)
			return err
		}
		return nil
	}

	sshArgs := append(append([]string{}, sshOptions...), r.sshHost())
	scpArgs := append([]string{"-q"}, sshOptions...)
	if seed {
		if i := strings.LastIndex(r.path, "/"); i > 0 {
			// Best effort; if it fails the upload below reports the real error.
			_, _ = runRemote("ssh", append(sshArgs, "mkdir -p -- "+remoteShellQuote(r.path[:i]))...)
		}
	}
	tmp := remote{host: r.host, path: r.path + suffix}
	if _, err := runRemote("scp", append(scpArgs, src, tmp.String())...); err != nil {
		return err
	}
	mv := "mv -f -- " + remoteShellQuote(tmp.path) + " " + remoteShellQuote(r.path)
	if _, err := runRemote("ssh", append(sshArgs, mv)...); err != nil {
		// The remote shell has no POSIX mv (e.g. Windows OpenSSH): fall back
		// to a direct, non-atomic upload rather than failing the sync.
		detail("%s", T("remote rename failed; falling back to direct upload", "远程重命名失败，改为直接上传"))
		_, _ = runRemote("ssh", append(sshArgs, "rm -f -- "+remoteShellQuote(tmp.path))...)
		if _, err := runRemote("scp", append(scpArgs, src, r.String())...); err != nil {
			return err
		}
	}
	return nil
}

// ---- sync engine ----

type syncMode int

const (
	syncBoth syncMode = iota
	syncPull
	syncPush
)

type syncReport struct {
	Remote  string           `json:"remote"`
	DryRun  bool             `json:"dry_run"`
	Seeded  bool             `json:"seeded"`
	Pulled  store.MergeStats `json:"pulled"`
	Pushed  store.MergeStats `json:"pushed"`
	Skipped bool             `json:"upload_skipped"`
}

func syncFail(format string, a ...any) error { return codeErr(exitSync, format, a...) }

// runSync performs one sync. Order matters for safety: the remote is only
// ever written after it was successfully read and merged, or after it was
// positively identified as not existing yet.
func runSync(mode syncMode, dryRun bool) (err error) {
	cfg, err := requireSyncConfig()
	if err != nil {
		return err
	}
	unlock, lerr := store.AcquireLock(syncLockFile(), 0, syncLockStale)
	if lerr != nil {
		return syncFail("%s", T("another sync is already running", "已有同步正在进行"))
	}
	defer unlock()
	if !dryRun {
		defer func() { recordSyncResult(err) }()
	}

	r := parseRemote(cfg.Remote)
	empty := store.New("").MergeFrom(store.New("")) // non-nil slices for JSON
	report := syncReport{Remote: cfg.Remote, DryRun: dryRun, Pulled: empty, Pushed: empty}

	local, err := loadStore()
	if err != nil {
		return err
	}

	var remoteStore *store.Store
	if mode != syncPush {
		tmp, terr := os.CreateTemp("", "jmp_pull_*.json")
		if terr != nil {
			return terr
		}
		tmpName := tmp.Name()
		tmp.Close()
		defer os.Remove(tmpName)

		switch ferr := r.fetch(tmpName); {
		case ferr == nil:
			if remoteStore, err = store.Load(tmpName); err != nil {
				return syncFail(T("the remote database is unreadable; nothing was changed (run 'jmp sync push' to replace it with the local copy): %v",
					"远程数据库无法读取，未做任何修改（可运行 'jmp sync push' 用本地副本覆盖）: %v"), err)
			}
			// What the remote would gain, measured on a throwaway copy.
			if scratch, serr := store.Load(tmpName); serr == nil {
				report.Pushed = scratch.MergeFrom(local)
			}
		case errors.Is(ferr, errRemoteMissing):
			if mode == syncPull {
				return syncFail("%s", T("the remote database does not exist yet; run 'jmp sync' to create it", "远程数据库尚不存在，运行 'jmp sync' 创建"))
			}
			report.Seeded = true
		default:
			return syncFail(T("pull failed; the remote was left untouched: %v", "拉取失败，远程未被修改: %v"), ferr)
		}
	}

	if remoteStore != nil {
		if dryRun {
			report.Pulled = local.MergeFrom(remoteStore) // in memory only
		} else if err := mutate(func(s *store.Store) error {
			report.Pulled = s.MergeFrom(remoteStore)
			return nil
		}); err != nil {
			return err
		}
	}

	switch {
	case mode == syncPull:
		report.Pushed = empty
		report.Skipped = true
	case mode == syncBoth && remoteStore != nil && report.Pushed.Changes() == 0:
		report.Skipped = true // the remote already has everything
	case dryRun:
	default:
		if err := r.push(dbPath, report.Seeded); err != nil {
			return syncFail(T("push failed: %v", "推送失败: %v"), err)
		}
	}

	printSyncReport(mode, report)
	return nil
}

func printSyncReport(mode syncMode, rep syncReport) {
	if jsonOut {
		printJSON(rep)
		return
	}
	list := func(dir string, st store.MergeStats) {
		for _, p := range st.Added {
			detail("%s + %s", dir, p)
		}
		for _, p := range st.Updated {
			detail("%s ~ %s", dir, p)
		}
		for _, p := range st.Removed {
			detail("%s - %s", dir, p)
		}
		for _, p := range st.AliasesAdded {
			detail("%s + @%s", dir, p)
		}
		for _, p := range st.AliasesUpdated {
			detail("%s ~ @%s", dir, p)
		}
		for _, p := range st.AliasesRemoved {
			detail("%s - @%s", dir, p)
		}
	}
	count := func(st store.MergeStats) string {
		return fmt.Sprintf("+%d ~%d -%d",
			len(st.Added)+len(st.AliasesAdded),
			len(st.Updated)+len(st.AliasesUpdated),
			len(st.Removed)+len(st.AliasesRemoved))
	}
	list("pull", rep.Pulled)
	list("push", rep.Pushed)

	prefix := ""
	if rep.DryRun {
		prefix = T("(dry run) ", "（试运行）")
	}
	switch {
	case mode == syncPush && rep.DryRun:
		info("%s%s", prefix, T("would upload the local database, replacing the remote copy", "将上传本地数据库并覆盖远程副本"))
	case mode == syncPush:
		info("%s", T("local database uploaded, replacing the remote copy", "已上传本地数据库并覆盖远程副本"))
	case rep.Seeded && rep.DryRun:
		info("%s%s", prefix, T("would create the remote database from the local copy", "将用本地副本创建远程数据库"))
	case rep.Seeded:
		info("%s", T("remote database created from the local copy", "已用本地副本创建远程数据库"))
	case mode == syncPull:
		info("%s%s: %s", prefix, T("pulled", "已拉取"), count(rep.Pulled))
	default:
		info("%s%s: %s %s, %s %s", prefix, T("synced", "已同步"),
			T("pulled", "拉取"), count(rep.Pulled), T("pushed", "推送"), count(rep.Pushed))
	}
}

// ---- commands ----

func buildSyncCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: T("sync the database across devices (SSH/SCP)", "多设备数据库同步 (SCP)"),
		Long: T(`Sync the jmp database across devices over SSH/SCP (or to a local path such
as a mounted share).

A sync pulls the remote copy, merges it, and uploads the result atomically.
Merge rules: weight = max, visits = max, last visit = latest. Removals
('jmp remove', 'jmp alias remove') propagate; an alias defined differently on
two devices resolves to the most recently set one. If the pull fails for any
reason other than "the remote file does not exist yet", nothing is uploaded.

Once a remote is configured the shell hook syncs in the background, at most
once per interval (also after a failure).

Examples:
  jmp sync set user@server:~/.local/share/jmp/db.json
  jmp sync set user@server:~/.local/share/jmp/db.json --interval 600
  jmp sync              # pull + merge + push
  jmp sync -n -v        # show what would change, change nothing
  jmp sync pull         # pull and merge only
  jmp sync push         # overwrite the remote with the local copy (no merge)
  jmp sync unset        # stop syncing`,
			`通过 SSH/SCP（或本地路径，如挂载的共享目录）跨设备同步 jmp 数据库。

一次同步会拉取远程副本、合并，再原子地上传结果。
合并策略：权重取最大值、访问次数取最大值、时间取最新。删除操作
（'jmp remove'、'jmp alias remove'）会传播；两台设备上定义不同的别名以最近
设置的为准。只要拉取失败的原因不是“远程文件尚不存在”，就不会上传任何内容。

配置远程路径后，shell hook 会在后台自动同步，每个间隔最多一次（失败后同样如此）。

示例：
  jmp sync set user@server:~/.local/share/jmp/db.json
  jmp sync set user@server:~/.local/share/jmp/db.json --interval 600
  jmp sync              # 拉取 + 合并 + 推送
  jmp sync -n -v        # 只显示将发生的变化，不做修改
  jmp sync pull         # 仅拉取并合并
  jmp sync push         # 用本地副本覆盖远程（不合并）
  jmp sync unset        # 停止同步`),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSync(syncBoth, dryRun)
		},
	}
	cmd.PersistentFlags().BoolVarP(&dryRun, "dry-run", "n", false, T("show what would change without changing anything", "只显示将发生的变化，不做修改"))

	cmd.AddCommand(
		buildSyncSetCmd(),
		buildSyncUnsetCmd(),
		&cobra.Command{
			Use:   "push",
			Short: T("overwrite the remote database with the local copy (no merge)", "用本地数据库覆盖远程（不合并）"),
			RunE: func(cmd *cobra.Command, args []string) error {
				return runSync(syncPush, dryRun)
			},
		},
		&cobra.Command{
			Use:   "pull",
			Short: T("pull the remote database and merge it", "拉取远程数据库并合并"),
			RunE: func(cmd *cobra.Command, args []string) error {
				return runSync(syncPull, dryRun)
			},
		},
		buildSyncAutoCmd(),
		buildSyncStatusCmd(),
	)
	return cmd
}

func buildSyncSetCmd() *cobra.Command {
	var interval int
	cmd := &cobra.Command{
		Use:   "set <remote> [interval]",
		Short: T("set the remote path and sync interval", "设置远程路径和同步间隔"),
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			badInterval := usageErr(fmt.Errorf(T("the interval must be a number of seconds >= %d", "间隔必须是不小于 %d 的秒数"), minSyncInterval))
			if len(args) == 2 {
				if cmd.Flags().Changed("interval") {
					return usageErr(fmt.Errorf("%s", T("give the interval either as an argument or with --interval, not both", "间隔只能用位置参数或 --interval 之一指定")))
				}
				n, err := strconv.Atoi(args[1])
				if err != nil {
					return badInterval
				}
				interval = n
			}
			if interval < minSyncInterval {
				return badInterval
			}
			target := strings.TrimSpace(args[0])
			if target == "" || strings.HasPrefix(target, "-") {
				return usageErr(fmt.Errorf("%s: %q", T("invalid remote", "无效的远程路径"), args[0]))
			}
			cfg := &SyncConfig{Remote: target, Interval: interval}
			if err := writeJSONFile(syncConfigFile(), cfg); err != nil {
				return err
			}
			if jsonOut {
				printJSON(cfg)
			} else {
				info("%s\n  %s %s\n  %s %ds", T("sync configured", "已配置同步"),
					T("remote:  ", "远程:"), cfg.Remote, T("interval:", "间隔:"), cfg.Interval)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&interval, "interval", defaultSyncInterval, T("minimum seconds between automatic syncs", "自动同步的最小间隔（秒）"))
	return cmd
}

func buildSyncUnsetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unset",
		Short: T("remove the sync configuration", "删除同步配置"),
		RunE: func(cmd *cobra.Command, args []string) error {
			err := os.Remove(syncConfigFile())
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			_ = os.Remove(syncStateFile())
			if jsonOut {
				printJSON(map[string]any{"configured": false})
			} else {
				info("%s", T("sync disabled; the local and remote databases were left as they are", "已停止同步；本地和远程数据库保持原样"))
			}
			return nil
		},
	}
}

func buildSyncAutoCmd() *cobra.Command {
	var foreground, force bool
	cmd := &cobra.Command{
		Use:   "auto",
		Short: T("sync if the interval has elapsed (called by the shell hook)", "自动同步（超过间隔才执行，供 shell hook 调用）"),
		Long: T(`Called by the shell hook. Returns immediately: when a sync is due it is
started as a detached background process, so the prompt never waits on the
network. A failed attempt is retried only after the interval has passed.`,
			`由 shell hook 调用，立即返回：需要同步时会启动一个独立的后台进程，
提示符不会等待网络。失败后要等间隔过去才会重试。`),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadSyncConfig()
			if err != nil || cfg == nil {
				return nil
			}
			if !force {
				st := loadSyncState()
				last := st.LastAttempt
				if st.LastSuccess.After(last) {
					last = st.LastSuccess
				}
				if !last.IsZero() && time.Since(last) < time.Duration(cfg.Interval)*time.Second {
					return nil
				}
				// Claim this interval before starting, so concurrent prompts
				// do not each launch a sync.
				st.LastAttempt = time.Now()
				saveSyncState(st)
			}
			if foreground {
				quiet = true
				_ = runSync(syncBoth, false) // the outcome is recorded in the sync state
				return nil
			}
			exe, err := os.Executable()
			if err != nil {
				return nil
			}
			child := exec.Command(exe, "--db", dbPath, "sync", "auto", "--foreground", "--force")
			if err := child.Start(); err == nil {
				_ = child.Process.Release()
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&foreground, "foreground", false, T("run the sync in this process instead of detaching", "在当前进程内同步，不转入后台"))
	cmd.Flags().BoolVar(&force, "force", false, T("ignore the interval", "忽略间隔限制"))
	_ = cmd.Flags().MarkHidden("force")
	return cmd
}

func buildSyncStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: T("show the sync configuration and state", "显示同步配置和状态"),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadSyncConfig()
			if err != nil {
				return err
			}
			if cfg == nil {
				if jsonOut {
					printJSON(map[string]any{"configured": false, "local_db": dbPath})
				} else {
					fmt.Fprintln(stdout, T("sync is not configured; run: jmp sync set <remote>", "同步未配置，运行: jmp sync set <remote>"))
				}
				return nil
			}
			st := loadSyncState()
			if jsonOut {
				stamp := func(t time.Time) any {
					if t.IsZero() {
						return nil
					}
					return t.Format(time.RFC3339)
				}
				printJSON(map[string]any{
					"configured":   true,
					"remote":       cfg.Remote,
					"interval":     cfg.Interval,
					"last_sync":    stamp(st.LastSuccess),
					"last_attempt": stamp(st.LastAttempt),
					"last_error":   st.LastError,
					"local_db":     dbPath,
				})
				return nil
			}
			human := func(t time.Time) string {
				if t.IsZero() {
					return T("never", "从未")
				}
				return t.Format("2006-01-02 15:04:05")
			}
			fmt.Fprintf(stdout, "%-14s %s\n", T("remote:", "远程:"), cfg.Remote)
			fmt.Fprintf(stdout, "%-14s %ds\n", T("interval:", "间隔:"), cfg.Interval)
			fmt.Fprintf(stdout, "%-14s %s\n", T("last sync:", "上次同步:"), human(st.LastSuccess))
			fmt.Fprintf(stdout, "%-14s %s\n", T("last attempt:", "上次尝试:"), human(st.LastAttempt))
			if st.LastError != "" {
				fmt.Fprintf(stdout, "%-14s %s\n", T("last error:", "上次错误:"), st.LastError)
			}
			fmt.Fprintf(stdout, "%-14s %s\n", T("local db:", "本地 DB:"), dbPath)
			return nil
		},
	}
}
