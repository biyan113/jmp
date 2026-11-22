package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/biyan113/jmp/internal/matcher"
	"github.com/biyan113/jmp/internal/shell"
	"github.com/biyan113/jmp/internal/store"
	"github.com/biyan113/jmp/internal/tui"
	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

var (
	dbPath   string
	jsonOut  bool
	verbose  bool
	quiet    bool
	skipPath string
)

// version is injected at build time via -ldflags "-X main.version=...".
// Defaults to "dev" for `go run` / untagged builds.
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:]))
}

func buildRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "jmp",
		Short: T("jmp - smart directory jumper (autojump, enhanced)", "jmp - 智能目录跳转工具 (autojump 增强版)"),
		Long: T(`jmp is a Go rewrite and enhancement of autojump.

Directories are ranked by frecency (frequency + recency decay). Works on
macOS / Linux / WSL / Windows.

Shell integration:
  eval "$(jmp init bash)"    # bash
  eval "$(jmp init zsh)"     # zsh
  jmp init fish | source     # fish
  Invoke-Expression (& jmp init powershell | Out-String)  # PowerShell

Once integrated, jump with the j command:
  j foo          # jump to the best match for "foo"
  j foo bar      # multiple keywords
  j              # open the TUI manager

Environment:
  JMP_DB     database file (same as --db)
  JMP_LANG   message language: en or zh (default: from LC_ALL / LANG)

Exit codes:
  0  success
  1  runtime error
  2  usage error
  3  no match / record not found
  4  sync could not reach or update the remote`,
			`jmp 是 autojump 的 Go 重写增强版本。

使用 frecency 算法（频率 + 时效衰减）排序，跨平台支持 macOS / Linux / WSL / Windows。

Shell 集成：
  eval "$(jmp init bash)"    # bash
  eval "$(jmp init zsh)"     # zsh
  jmp init fish | source     # fish
  Invoke-Expression (& jmp init powershell | Out-String)  # PowerShell

集成后直接使用 j 命令跳转：
  j foo          # 跳转到最匹配 "foo" 的目录
  j foo bar      # 多关键词匹配
  j              # 打开 TUI 管理界面

环境变量：
  JMP_DB     数据库文件（等同 --db）
  JMP_LANG   消息语言：en 或 zh（默认取自 LC_ALL / LANG）

退出码：
  0  成功
  1  运行时错误
  2  用法错误
  3  无匹配 / 记录不存在
  4  同步无法连接或更新远程`),
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTUI()
		},
	}
	root.SetVersionTemplate("jmp {{.Version}}\n")
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageErr(err) })

	pf := root.PersistentFlags()
	pf.StringVar(&dbPath, "db", defaultDBPath(), T("database file path (env: JMP_DB)", "数据库文件路径（环境变量: JMP_DB）"))
	pf.BoolVar(&jsonOut, "json", false, T("output JSON", "JSON 格式输出"))
	pf.BoolVarP(&verbose, "verbose", "v", false, T("show more detail", "显示更多细节"))
	// No -q shorthand: `jmp list -q <keyword>` already owns it.
	pf.BoolVar(&quiet, "quiet", false, T("suppress status messages", "不输出状态信息"))
	root.Flags().Bool("tui", false, T("open the TUI manager (the default)", "打开 TUI 管理界面（默认行为）"))

	root.AddCommand(
		buildJumpCmd(),
		buildAddCmd(),
		buildRemoveCmd(),
		buildListCmd(),
		buildInitCmd(),
		buildTUICmd(),
		buildStatsCmd(),
		buildSyncCmd(),
		buildConfigCmd(),
		buildDoctorCmd(),
		buildCleanCmd(),
		buildAliasCmd(),
		buildImportCmd(),
		buildCompleteCmd(),
		buildSuggestCmd(),
		buildVersionCmd(),
		buildManCmd(),
	)
	strictArgs(root)
	return root
}

// ---- store access ----

// loadStore opens the database for reading, transparently recovering from a
// corrupt file.
func loadStore() (*store.Store, error) {
	s, note, err := store.LoadOrRecover(dbPath)
	if note != "" {
		warn("%s", note)
	}
	return s, err
}

// mutate runs fn as one locked read-modify-write cycle on the database.
func mutate(fn func(s *store.Store) error) error {
	s := store.New(dbPath)
	err := s.Update(func() error { return fn(s) })
	if errors.Is(err, store.ErrCorrupt) {
		if s, err = loadStore(); err != nil {
			return err
		}
		err = s.Update(func() error { return fn(s) })
	}
	return err
}

// ---- jump ----

func buildJumpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "jump <query...>",
		Short: T("print the best matching path (used by the shell function)", "输出最匹配路径（供 shell 函数使用）"),
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runJump(args)
		},
	}
	cmd.Flags().StringVar(&skipPath, "skip", "", T("skip this path when other matches exist", "有其他匹配时跳过指定路径"))
	return cmd
}

// runJump is read-only: it never prunes the database. Directories that do not
// exist right now (an unmounted volume, another machine's path) are merely
// skipped by the matcher; removing them is `jmp clean`'s job.
func runJump(terms []string) error {
	s, err := loadStore()
	if err != nil {
		return err
	}

	if len(terms) == 1 && strings.HasPrefix(terms[0], "@") {
		if path, ok := s.ResolveAlias(terms[0]); ok && store.IsExistingDir(path) {
			if jsonOut {
				printJSON(map[string]any{"path": path, "alias": terms[0]})
				return nil
			}
			fmt.Fprintln(stdout, path)
			return nil
		}
	}

	results := matcher.Rank(s, terms)

	// Skip current directory only when other matches exist (enables cycling)
	if skipPath != "" && len(results) > 1 {
		skip := store.NormalizePath(skipPath)
		filtered := make([]matcher.Result, 0, len(results))
		for _, r := range results {
			if r.Entry.Path != skip {
				filtered = append(filtered, r)
			}
		}
		if len(filtered) > 0 {
			results = filtered
		}
	}

	if len(results) == 0 {
		if jsonOut {
			printJSON(map[string]any{"error": "no match", "query": terms, "code": exitNotFound})
		} else {
			fmt.Fprintf(stderr, "jmp: %s: %s\n", T("no match for", "没有匹配"), strings.Join(terms, " "))
			printSuggestions(stderr, s, terms)
		}
		return silentExit(exitNotFound)
	}

	best := results[0]
	if jsonOut {
		printJSON(map[string]any{
			"path":  best.Entry.Path,
			"score": best.Score,
		})
		return nil
	}
	fmt.Fprintln(stdout, best.Entry.Path)
	return nil
}

// ---- add ----

func buildAddCmd() *cobra.Command {
	var weight float64
	cmd := &cobra.Command{
		Use:   "add <path>...",
		Short: T("record a visit to one or more directories", "添加或更新路径记录"),
		Long: T(`Record a visit to each directory (weight +1), creating the entry if needed.

With -w the weight is set to exactly that value instead, overwriting the
current one. Paths that are not existing directories are rejected.`,
			`为每个目录记录一次访问（权重 +1），不存在则创建记录。

使用 -w 时改为把权重精确设置为该值（覆盖现有权重）。不是现存目录的路径会被拒绝。`),
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			setWeight := cmd.Flags().Changed("weight")
			if setWeight && weight <= 0 {
				return usageErr(fmt.Errorf("%s", T("--weight must be greater than 0", "--weight 必须大于 0")))
			}
			added := []string{}
			rejected := []string{}
			err := mutate(func(s *store.Store) error {
				added, rejected = added[:0], rejected[:0]
				for _, arg := range args {
					path := store.NormalizePath(arg)
					if !store.IsExistingDir(path) {
						rejected = append(rejected, arg)
						continue
					}
					if setWeight {
						s.AddManual(path, weight)
					} else {
						s.Add(path)
					}
					added = append(added, path)
				}
				return nil
			})
			if err != nil {
				return err
			}
			if jsonOut {
				printJSON(map[string]any{"added": added, "rejected": rejected})
			} else {
				for _, p := range rejected {
					fmt.Fprintf(stderr, "jmp: %s: %s\n", T("not a directory", "不是目录"), p)
				}
				for _, p := range added {
					detail("%s: %s", T("added", "已添加"), p)
				}
			}
			if len(rejected) > 0 {
				return silentExit(exitError)
			}
			return nil
		},
	}
	cmd.Flags().Float64VarP(&weight, "weight", "w", 0, T("set the weight to this value (overwrites; default: +1 per visit)", "把权重设置为该值（覆盖现有值；默认每次访问 +1）"))
	return cmd
}

// ---- remove ----

func buildRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "remove <path>...",
		Aliases: []string{"rm", "del"},
		Short:   T("remove path records", "删除路径记录"),
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			removed := []string{}
			missing := []string{}
			err := mutate(func(s *store.Store) error {
				removed, missing = removed[:0], missing[:0]
				for _, arg := range args {
					if s.Remove(arg) {
						removed = append(removed, store.NormalizePath(arg))
					} else {
						missing = append(missing, arg)
					}
				}
				return nil
			})
			if err != nil {
				return err
			}
			if jsonOut {
				printJSON(map[string]any{"removed": removed, "not_found": missing})
			} else {
				for _, p := range missing {
					fmt.Fprintf(stderr, "jmp: %s: %s\n", T("path not found", "路径不存在于数据库"), p)
				}
				for _, p := range removed {
					info("%s: %s", T("removed", "已删除"), p)
				}
			}
			if len(missing) > 0 {
				return silentExit(exitNotFound)
			}
			return nil
		},
	}
}

// ---- list ----

func buildListCmd() *cobra.Command {
	var limit int
	var query []string
	var pathsOnly, noHeader bool
	cmd := &cobra.Command{
		Use:     "list [keyword...]",
		Aliases: []string{"ls"},
		Short:   T("list path records", "列出所有路径记录"),
		Long: T(`List recorded directories, best first. Keywords filter the list.

Use --paths for one bare path per line (pipe-friendly), --no-header to drop
the table header, or --json for structured output.`,
			`按得分从高到低列出记录的目录，可用关键词过滤。

--paths 每行只输出一个路径（适合管道），--no-header 去掉表头，--json 输出结构化数据。`),
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if limit < 0 {
				return usageErr(fmt.Errorf("%s", T("--limit must not be negative", "--limit 不能为负数")))
			}
			s, err := loadStore()
			if err != nil {
				return err
			}
			terms := append(append([]string{}, query...), args...)
			var entries []*store.Entry
			if len(terms) > 0 {
				entries = s.QueryEntries(terms)
			} else {
				entries = s.All()
			}
			if limit > 0 && len(entries) > limit {
				entries = entries[:limit]
			}

			if jsonOut {
				type row struct {
					Path      string  `json:"path"`
					Weight    float64 `json:"weight"`
					Frecency  float64 `json:"frecency"`
					Visits    int64   `json:"visits"`
					LastVisit string  `json:"last_visit"`
				}
				rows := make([]row, len(entries))
				for i, e := range entries {
					rows[i] = row{
						Path:      e.Path,
						Weight:    e.Weight,
						Frecency:  e.Frecency(),
						Visits:    e.Visits,
						LastVisit: e.LastVisit.Format("2006-01-02T15:04:05Z07:00"),
					}
				}
				printJSON(rows)
				return nil
			}
			if pathsOnly {
				for _, e := range entries {
					fmt.Fprintln(stdout, e.Path)
				}
				return nil
			}
			if !noHeader {
				fmt.Fprintf(stdout, "%-10s %-8s %-16s %s\n", "SCORE", "VISITS", "LAST VISIT", "PATH")
			}
			for _, e := range entries {
				fmt.Fprintf(stdout, "%-10.1f %-8d %-16s %s\n",
					e.Frecency(),
					e.Visits,
					e.LastVisit.Format("2006-01-02 15:04"),
					e.Path,
				)
			}
			return nil
		},
	}
	cmd.Flags().IntVarP(&limit, "limit", "n", 0, T("show at most this many entries", "最多显示条数"))
	cmd.Flags().StringArrayVarP(&query, "query", "q", nil, T("filter keyword (same as a positional keyword)", "过滤关键词（等同位置参数）"))
	cmd.Flags().BoolVarP(&pathsOnly, "paths", "p", false, T("print bare paths only, one per line", "只输出路径，每行一个"))
	cmd.Flags().BoolVar(&noHeader, "no-header", false, T("omit the table header", "不输出表头"))
	return cmd
}

// ---- init ----

func buildInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init <shell>",
		Short: T("print the shell integration script", "输出 shell 集成脚本"),
		Long: T(`Print the integration script for a shell.
Supported: bash, zsh, fish, powershell

Usage:
  eval "$(jmp init bash)"
  eval "$(jmp init zsh)"
  jmp init fish | source
  Invoke-Expression (& jmp init powershell | Out-String)`,
			`输出对应 shell 的集成脚本。
支持: bash, zsh, fish, powershell

用法:
  eval "$(jmp init bash)"
  eval "$(jmp init zsh)"
  jmp init fish | source
  Invoke-Expression (& jmp init powershell | Out-String)`),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			script, err := shell.InitScript(args[0], stableBinPath())
			if err != nil {
				return usageErr(err)
			}
			fmt.Fprint(stdout, script)
			return nil
		},
	}
}

// stableBinPath returns the path the shell integration should invoke.
//
// os.Executable resolves symlinks on Linux, which for package managers such
// as Homebrew yields a versioned Cellar path that disappears on upgrade. When
// the jmp found on PATH is the same file, prefer that stable location.
func stableBinPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "jmp"
	}
	if onPath, err := exec.LookPath("jmp"); err == nil {
		if abs, err := filepath.Abs(onPath); err == nil {
			a, errA := os.Stat(abs)
			b, errB := os.Stat(exe)
			if errA == nil && errB == nil && os.SameFile(a, b) {
				return abs
			}
		}
	}
	return exe
}

// ---- tui ----

func buildTUICmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: T("open the TUI path manager", "打开 TUI 路径管理界面"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTUI()
		},
	}
}

func runTUI() error {
	s, err := loadStore()
	if err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	selected, err := tui.Run(s, dbPath, cfg.Color, version)
	if err != nil {
		return err
	}
	if selected != "" {
		fmt.Fprintln(stdout, selected)
	}
	return nil
}

// ---- config ----

func buildConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: T("manage local configuration", "管理本地配置"),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			if jsonOut {
				printJSON(cfg)
				return nil
			}
			fmt.Fprintf(stdout, "color: %s\n", cfg.Color)
			return nil
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "color <auto|always|never|ansi256|truecolor>",
		Short: T("set the TUI color mode", "设置 TUI 颜色模式"),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !validColorMode(args[0]) {
				return usageErr(fmt.Errorf("color must be one of: auto, always, never, ansi256, truecolor"))
			}
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			cfg.Color = args[0]
			if err := saveConfig(cfg); err != nil {
				return err
			}
			if jsonOut {
				printJSON(cfg)
			} else {
				info("color: %s", cfg.Color)
			}
			return nil
		},
	})
	return cmd
}

// ---- doctor / clean ----

func brokenAliases(s *store.Store) map[string]string {
	out := make(map[string]string)
	for name, path := range s.AllAliases() {
		if !store.IsExistingDir(path) {
			out[name] = path
		}
	}
	return out
}

func buildDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: T("check database health", "检查数据库健康状态"),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			missing := s.MissingEntries()
			aliases := s.AllAliases()
			broken := brokenAliases(s)
			if jsonOut {
				printJSON(map[string]any{
					"db_path":          dbPath,
					"entries":          len(s.All()),
					"missing":          len(missing),
					"aliases":          len(aliases),
					"broken_aliases":   len(broken),
					"missing_entries":  missing,
					"broken_alias_map": broken,
				})
				return nil
			}
			fmt.Fprintf(stdout, "%-16s %s\n", T("database:", "数据库:"), dbPath)
			fmt.Fprintf(stdout, "%-16s %d\n", T("entries:", "路径:"), len(s.All()))
			fmt.Fprintf(stdout, "%-16s %d\n", T("missing:", "缺失:"), len(missing))
			fmt.Fprintf(stdout, "%-16s %d\n", T("aliases:", "别名:"), len(aliases))
			fmt.Fprintf(stdout, "%-16s %d\n", T("broken aliases:", "坏别名:"), len(broken))
			for _, e := range missing {
				fmt.Fprintf(stdout, "missing: %s\n", e.Path)
			}
			for _, name := range sortedKeys(broken) {
				fmt.Fprintf(stdout, "broken alias: @%s -> %s\n", name, broken[name])
			}
			return nil
		},
	}
}

func buildCleanCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "clean",
		Short: T("remove records of missing directories and broken aliases", "删除不存在的路径记录和失效别名"),
		Long: T(`Remove records whose directory no longer exists, and aliases pointing nowhere.

This is the only command that prunes missing directories; jumping never does.
Make sure removable or network volumes are mounted first, or use --dry-run to
see exactly what would go. The cleanup is local: with sync configured, paths
that are valid on another device come back on the next sync.`,
			`删除目录已不存在的记录，以及指向无效位置的别名。

只有这个命令会清理缺失目录，跳转时不会。请先确认移动硬盘、网络盘已挂载，
或用 --dry-run 查看将被删除的内容。清理只作用于本机：配置了同步时，
在其他设备上有效的路径会在下次同步时回来。`),
		RunE: func(cmd *cobra.Command, args []string) error {
			var paths, aliases []string
			if dryRun {
				s, err := loadStore()
				if err != nil {
					return err
				}
				for _, e := range s.MissingEntries() {
					paths = append(paths, e.Path)
				}
				sort.Strings(paths)
				aliases = sortedKeys(brokenAliases(s))
			} else {
				err := mutate(func(s *store.Store) error {
					paths, aliases = s.CleanMissing()
					return nil
				})
				if err != nil {
					return err
				}
			}
			if jsonOut {
				if paths == nil {
					paths = []string{}
				}
				if aliases == nil {
					aliases = []string{}
				}
				if dryRun {
					printJSON(map[string]any{"dry_run": true, "missing": paths, "broken_aliases": aliases})
				} else {
					printJSON(map[string]any{"removed_paths": paths, "removed_aliases": aliases})
				}
				return nil
			}
			show := info // a dry run exists to show the list
			if !dryRun {
				show = detail
			}
			for _, p := range paths {
				show("%s", p)
			}
			for _, a := range aliases {
				show("@%s", a)
			}
			if dryRun {
				info(T("would remove %d paths and %d aliases", "将删除 %d 条路径和 %d 个别名"), len(paths), len(aliases))
			} else {
				info(T("removed %d paths and %d aliases", "已删除 %d 条路径和 %d 个别名"), len(paths), len(aliases))
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, T("list what would be removed without changing anything", "只列出将删除的记录，不做修改"))
	return cmd
}

// ---- alias ----

func buildAliasCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "alias",
		Short: T("manage @alias shortcuts", "管理 @alias 快捷跳转"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAliasList()
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "set <name> <path>",
		Short: T("create or update an alias", "设置别名"),
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(args[0])), "@")
			if !store.ValidAliasName(name) {
				return usageErr(fmt.Errorf(T("invalid alias name %q (use letters, digits, '.', '_' or '-')",
					"无效的别名 %q（只能使用字母、数字、'.'、'_'、'-'）"), args[0]))
			}
			path := store.NormalizePath(args[1])
			if !store.IsExistingDir(path) {
				return fmt.Errorf("%s: %s", T("not a directory", "不是目录"), args[1])
			}
			err := mutate(func(s *store.Store) error {
				if err := s.SetAlias(name, path); err != nil {
					return err
				}
				// Make the target jumpable, but never touch an existing weight.
				s.Ensure(path, 10)
				return nil
			})
			if err != nil {
				return err
			}
			if jsonOut {
				printJSON(map[string]any{"alias": name, "path": path})
			} else {
				info("@%s -> %s", name, path)
			}
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:     "remove <name>",
		Aliases: []string{"rm", "del"},
		Short:   T("remove an alias", "删除别名"),
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimPrefix(args[0], "@")
			found := false
			err := mutate(func(s *store.Store) error {
				found = s.RemoveAlias(name)
				return nil
			})
			if err != nil {
				return err
			}
			if !found {
				return codeErr(exitNotFound, "%s: %s", T("alias not found", "别名不存在"), args[0])
			}
			if jsonOut {
				printJSON(map[string]any{"removed": name})
			} else {
				info("%s @%s", T("removed", "已删除"), name)
			}
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: T("list aliases", "列出别名"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAliasList()
		},
	})
	return cmd
}

func runAliasList() error {
	s, err := loadStore()
	if err != nil {
		return err
	}
	aliases := s.AllAliases()
	if jsonOut {
		printJSON(aliases)
		return nil
	}
	for _, name := range sortedKeys(aliases) {
		fmt.Fprintf(stdout, "@%-16s %s\n", name, aliases[name])
	}
	return nil
}

// ---- import ----

func buildImportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "import <file|->",
		Short: T("import paths from a text or history file", "从文本或常见历史文件导入路径"),
		Long: T(`Import directories mentioned in a text or shell-history file ("-" reads
standard input). Only directories that exist are imported, each once, and
entries already in the database are left untouched.`,
			`导入文本或 shell 历史文件中出现的目录（"-" 表示从标准输入读取）。
只导入现存的目录，每个只计一次；数据库中已有的记录保持不变。`),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var in io.Reader = stdin
			source := "-"
			if args[0] != "-" {
				file, err := expandPath(args[0])
				if err != nil {
					return err
				}
				f, err := os.Open(file)
				if err != nil {
					return err
				}
				defer f.Close()
				in, source = f, file
			}

			// Collect candidate directories first so the database lock is held
			// only for the brief write.
			var dirs []string
			seen := map[string]bool{}
			scanner := bufio.NewScanner(in)
			scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
			for scanner.Scan() {
				for _, path := range extractPaths(scanner.Text()) {
					path = store.NormalizePath(path)
					if seen[path] {
						continue
					}
					seen[path] = true
					if store.IsExistingDir(path) {
						dirs = append(dirs, path)
					}
				}
			}
			if err := scanner.Err(); err != nil {
				return err
			}

			imported, existing := 0, 0
			err := mutate(func(s *store.Store) error {
				imported, existing = 0, 0
				for _, path := range dirs {
					if s.Ensure(path, 1) {
						imported++
						detail("%s: %s", T("imported", "已导入"), path)
					} else {
						existing++
					}
				}
				return nil
			})
			if err != nil {
				return err
			}
			if jsonOut {
				printJSON(map[string]any{"imported": imported, "already_known": existing, "file": source})
			} else {
				info(T("imported %d new paths (%d already known)", "导入 %d 条新路径（%d 条已存在）"), imported, existing)
			}
			return nil
		},
	}
}

// ---- completion / suggestions ----

func buildCompleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "complete [prefix]",
		Short:  T("print shell completion candidates", "输出 shell 补全候选"),
		Hidden: true,
		Args:   cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			prefix := ""
			if len(args) == 1 {
				prefix = strings.ToLower(args[0])
			}
			s, err := loadStore()
			if err != nil {
				return err
			}
			seen := map[string]bool{}
			aliases := s.AllAliases()
			for _, name := range sortedKeys(aliases) {
				if !store.IsExistingDir(aliases[name]) {
					continue
				}
				candidate := "@" + name
				if strings.HasPrefix(candidate, prefix) {
					fmt.Fprintln(stdout, candidate)
					seen[candidate] = true
				}
			}
			for _, e := range s.All() {
				if !store.IsExistingDir(e.Path) {
					continue
				}
				base := filepath.Base(e.Path)
				if base != "" && strings.HasPrefix(strings.ToLower(base), prefix) && !seen[base] {
					fmt.Fprintln(stdout, base)
					seen[base] = true
				}
			}
			return nil
		},
	}
}

func buildSuggestCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "suggest <query...>",
		Short:  T("print approximate matches", "输出近似匹配建议"),
		Hidden: true,
		Args:   cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			suggestions := matcher.Suggest(s, args, 5)
			for _, r := range suggestions {
				fmt.Fprintln(stdout, r.Entry.Path)
			}
			return nil
		},
	}
}

// ---- version / man ----

func buildVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: T("print the jmp version (same as --version)", "输出 jmp 版本（等同 --version）"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if jsonOut {
				printJSON(map[string]any{"version": version})
				return nil
			}
			fmt.Fprintln(stdout, "jmp", version)
			return nil
		},
	}
}

func buildManCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "man <dir>",
		Short:  T("generate man pages into a directory", "生成 man 手册页到指定目录"),
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := os.MkdirAll(args[0], 0o755); err != nil {
				return err
			}
			root := cmd.Root()
			root.DisableAutoGenTag = true
			header := &doc.GenManHeader{Title: "JMP", Section: "1", Source: "jmp " + version, Manual: "jmp manual"}
			if err := doc.GenManTree(root, header, args[0]); err != nil {
				return err
			}
			info(T("man pages written to %s", "man 手册页已写入 %s"), args[0])
			return nil
		},
	}
}

// ---- stats ----

func buildStatsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stats",
		Short: T("show database statistics", "显示数据库统计信息"),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			entries := s.All()
			totalWeight := 0.0
			totalVisits := int64(0)
			for _, e := range entries {
				totalWeight += e.Weight
				totalVisits += e.Visits
			}

			if jsonOut {
				printJSON(map[string]any{
					"entries":      len(entries),
					"total_weight": totalWeight,
					"total_visits": totalVisits,
					"db_path":      dbPath,
				})
				return nil
			}

			fmt.Fprintf(stdout, "%-14s %s\n", T("database:", "数据库路径:"), dbPath)
			fmt.Fprintf(stdout, "%-14s %d\n", T("entries:", "路径条数:"), len(entries))
			fmt.Fprintf(stdout, "%-14s %.1f\n", T("total weight:", "总权重:"), totalWeight)
			fmt.Fprintf(stdout, "%-14s %d\n", T("total visits:", "总访问次数:"), totalVisits)
			if len(entries) > 0 {
				fmt.Fprintf(stdout, "\nTop 10:\n")
				fmt.Fprintf(stdout, "%-10s %s\n", "SCORE", "PATH")
				for _, e := range entries[:min(10, len(entries))] {
					fmt.Fprintf(stdout, "%-10.1f %s\n", e.Frecency(), e.Path)
				}
			}
			return nil
		},
	}
}

// ---- helpers ----

func printSuggestions(w io.Writer, s *store.Store, terms []string) {
	results := matcher.Suggest(s, terms, 3)
	if len(results) == 0 {
		return
	}
	fmt.Fprintln(w, T("suggestions:", "建议:"))
	for _, r := range results {
		fmt.Fprintf(w, "  %s\n", r.Entry.Path)
	}
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func expandPath(path string) (string, error) {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, path[2:])
	}
	return filepath.Abs(path)
}

// isAbsish reports whether s looks like an absolute or home-relative path
// on the current platform: "~/" anywhere, unix "/", or a Windows drive
// like "C:\" / "C:/". This keeps `jmp import` portable across OSes.
func isAbsish(s string) bool {
	if strings.HasPrefix(s, "~/") || strings.HasPrefix(s, "/") || s == "~" {
		return true
	}
	// Windows drive letter: C:\ or C:/
	if len(s) >= 3 && s[1] == ':' && (s[2] == '\\' || s[2] == '/') {
		if c := s[0]; (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			return true
		}
	}
	return false
}

func extractPaths(line string) []string {
	fields := strings.Fields(line)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.Trim(f, "\"'();")
		if strings.HasPrefix(f, "path:") {
			f = strings.TrimPrefix(f, "path:")
		}
		if isAbsish(f) {
			if path, err := expandPath(f); err == nil {
				out = append(out, path)
			}
		}
	}
	if len(out) == 0 && len(fields) >= 2 {
		candidate := strings.Trim(fields[len(fields)-1], "\"'();")
		if isAbsish(candidate) {
			if path, err := expandPath(candidate); err == nil {
				out = append(out, path)
			}
		}
	}
	return out
}
