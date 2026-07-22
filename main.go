package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/bytenote/jmp/internal/matcher"
	"github.com/bytenote/jmp/internal/shell"
	"github.com/bytenote/jmp/internal/store"
	"github.com/bytenote/jmp/internal/tui"
	"github.com/spf13/cobra"
)

var (
	dbPath   string
	jsonOut  bool
	skipPath string
)

// version is injected at build time via -ldflags "-X main.version=...".
// Defaults to "dev" for `go run` / untagged builds.
var version = "dev"

func main() {
	root := buildRoot()
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func buildRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "jmp",
		Short: "jmp - 智能目录跳转工具 (autojump 增强版)",
		Long: `jmp 是 autojump 的 Go 重写增强版本。

使用 frecency 算法（频率 + 时效衰减）排序，跨平台支持 macOS / Linux / WSL / Windows。

Shell 集成：
  eval "$(jmp init bash)"    # bash
  eval "$(jmp init zsh)"     # zsh
  jmp init fish | source     # fish
  Invoke-Expression (& jmp init powershell | Out-String)  # PowerShell

集成后直接使用 j 命令跳转：
  j foo          # 跳转到最匹配 "foo" 的目录
  j foo bar      # 多关键词匹配
  j              # 打开 TUI 管理界面`,
		// 直接运行 jmp 不带子命令时，如果有参数就执行跳转
		RunE: func(cmd *cobra.Command, args []string) error {
			if tui_, _ := cmd.Flags().GetBool("tui"); tui_ {
				return runTUI()
			}
			if len(args) == 0 {
				return runTUI()
			}
			return runJump(args)
		},
	}

	root.PersistentFlags().StringVar(&dbPath, "db", store.DefaultPath(), "数据库文件路径")
	root.PersistentFlags().BoolVar(&jsonOut, "json", false, "JSON 格式输出")
	root.Flags().Bool("tui", false, "打开 TUI 管理界面")

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
	)

	return root
}

// ---- jump ----

func buildJumpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "jump <query...>",
		Short: "输出最匹配路径（供 shell 函数使用）",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runJump(args)
		},
	}
	cmd.Flags().StringVar(&skipPath, "skip", "", "跳过指定路径")
	return cmd
}

func runJump(terms []string) error {
	s, err := loadStore()
	if err != nil {
		return err
	}

	if len(terms) == 1 && strings.HasPrefix(terms[0], "@") {
		if path, ok := s.ResolveAlias(terms[0]); ok {
			if jsonOut {
				printJSON(map[string]any{"path": path, "alias": terms[0]})
				return nil
			}
			fmt.Println(path)
			return nil
		}
	}

	results := matcher.Rank(s, terms)

	// Skip current directory only when other matches exist (enables cycling)
	if skipPath != "" && len(results) > 1 {
		filtered := make([]matcher.Result, 0, len(results))
		for _, r := range results {
			if r.Entry.Path != skipPath {
				filtered = append(filtered, r)
			}
		}
		if len(filtered) > 0 {
			results = filtered
		}
	}

	if len(results) == 0 {
		if jsonOut {
			printJSON(map[string]any{"error": "no match", "query": terms})
		} else {
			fmt.Fprintf(os.Stderr, "jmp: no match for: %s\n", strings.Join(terms, " "))
			printSuggestions(os.Stderr, s, terms)
		}
		os.Exit(1)
	}

	best := results[0]
	if jsonOut {
		printJSON(map[string]any{
			"path":  best.Entry.Path,
			"score": best.Score,
		})
		return nil
	}
	fmt.Println(best.Entry.Path)
	return nil
}

// ---- add ----

func buildAddCmd() *cobra.Command {
	var weight float64
	cmd := &cobra.Command{
		Use:   "add <path>",
		Short: "添加或更新路径记录",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			path := args[0]
			if weight > 0 {
				s.AddManual(path, weight)
			} else {
				s.Add(path)
			}
			if err := s.Save(); err != nil {
				return err
			}
			if jsonOut {
				printJSON(map[string]any{"added": path})
			}
			return nil
		},
	}
	cmd.Flags().Float64VarP(&weight, "weight", "w", 0, "指定初始权重（默认自动累加）")
	return cmd
}

// ---- remove ----

func buildRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "remove <path>",
		Aliases: []string{"rm", "del"},
		Short:   "删除路径记录",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			removed := s.Remove(args[0])
			if !removed {
				if jsonOut {
					printJSON(map[string]any{"error": "not found", "path": args[0]})
				} else {
					fmt.Fprintf(os.Stderr, "jmp: path not found: %s\n", args[0])
				}
				os.Exit(2)
			}
			if err := s.Save(); err != nil {
				return err
			}
			if jsonOut {
				printJSON(map[string]any{"removed": args[0]})
			} else {
				fmt.Printf("已删除: %s\n", args[0])
			}
			return nil
		},
	}
}

// ---- list ----

func buildListCmd() *cobra.Command {
	var limit int
	var query []string
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "列出所有路径记录",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			var entries []*store.Entry
			if len(query) > 0 {
				entries = s.QueryEntries(query)
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
			fmt.Printf("%-10s %-8s %-16s %s\n", "SCORE", "VISITS", "LAST VISIT", "PATH")
			fmt.Println(strings.Repeat("-", 80))
			for _, e := range entries {
				fmt.Printf("%-10.1f %-8d %-16s %s\n",
					e.Frecency(),
					e.Visits,
					e.LastVisit.Format("01-02 15:04"),
					e.Path,
				)
			}
			return nil
		},
	}
	cmd.Flags().IntVarP(&limit, "limit", "n", 0, "最多显示条数")
	cmd.Flags().StringArrayVarP(&query, "query", "q", nil, "过滤关键词")
	return cmd
}

// ---- init ----

func buildInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init <shell>",
		Short: "输出 shell 集成脚本",
		Long: `输出对应 shell 的集成脚本。
支持: bash, zsh, fish, powershell

用法:
  eval "$(jmp init bash)"
  eval "$(jmp init zsh)"
  jmp init fish | source
  Invoke-Expression (& jmp init powershell | Out-String)`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			bin, err := os.Executable()
			if err != nil {
				bin = "jmp"
			}
			script, err := shell.InitScript(args[0], bin)
			if err != nil {
				return err
			}
			fmt.Print(script)
			return nil
		},
	}
}

// ---- tui ----

func buildTUICmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "打开 TUI 路径管理界面",
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
		fmt.Println(selected)
	}
	return nil
}

// ---- config ----

func buildConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "管理本地配置",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			if jsonOut {
				printJSON(cfg)
				return nil
			}
			fmt.Printf("color: %s\n", cfg.Color)
			return nil
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "color <auto|always|never|ansi256|truecolor>",
		Short: "设置 TUI 颜色模式",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !validColorMode(args[0]) {
				return fmt.Errorf("color must be one of: auto, always, never, ansi256, truecolor")
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
				fmt.Printf("color: %s\n", cfg.Color)
			}
			return nil
		},
	})
	return cmd
}

// ---- doctor / clean ----

func buildDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "检查数据库健康状态",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			missing := s.MissingEntries()
			aliases := s.AllAliases()
			brokenAliases := make(map[string]string)
			for name, path := range aliases {
				if _, err := os.Stat(path); os.IsNotExist(err) {
					brokenAliases[name] = path
				}
			}
			report := map[string]any{
				"db_path":        dbPath,
				"entries":        len(s.All()),
				"missing":        len(missing),
				"aliases":        len(aliases),
				"broken_aliases": len(brokenAliases),
			}
			if jsonOut {
				report["missing_entries"] = missing
				report["broken_alias_map"] = brokenAliases
				printJSON(report)
				return nil
			}
			fmt.Printf("数据库: %s\n", dbPath)
			fmt.Printf("路径:   %d\n", report["entries"])
			fmt.Printf("缺失:   %d\n", len(missing))
			fmt.Printf("别名:   %d\n", len(aliases))
			fmt.Printf("坏别名: %d\n", len(brokenAliases))
			for _, e := range missing {
				fmt.Printf("missing: %s\n", e.Path)
			}
			for name, path := range brokenAliases {
				fmt.Printf("broken alias: @%s -> %s\n", name, path)
			}
			return nil
		},
	}
}

func buildCleanCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "clean",
		Short: "删除不存在的路径记录和失效别名",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			if dryRun {
				missing := s.MissingEntries()
				aliases := s.AllAliases()
				broken := make([]string, 0)
				for name, path := range aliases {
					if _, err := os.Stat(path); os.IsNotExist(err) {
						broken = append(broken, name)
					}
				}
				if jsonOut {
					printJSON(map[string]any{"missing": missing, "broken_aliases": broken})
				} else {
					fmt.Printf("would remove %d paths and %d aliases\n", len(missing), len(broken))
				}
				return nil
			}
			removedPaths, removedAliases := s.CleanMissing()
			if err := s.Save(); err != nil {
				return err
			}
			if jsonOut {
				printJSON(map[string]any{"removed_paths": removedPaths, "removed_aliases": removedAliases})
			} else {
				fmt.Printf("removed %d paths and %d aliases\n", len(removedPaths), len(removedAliases))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "只显示将删除的记录")
	return cmd
}

// ---- alias ----

func buildAliasCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "alias",
		Short: "管理 @alias 快捷跳转",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAliasList()
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "set <name> <path>",
		Short: "设置别名",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			path := store.NormalizePath(args[1])
			if _, err := os.Stat(path); err != nil {
				return err
			}
			s.SetAlias(args[0], path)
			s.AddManual(path, 10)
			if err := s.Save(); err != nil {
				return err
			}
			if jsonOut {
				printJSON(map[string]any{"alias": args[0], "path": path})
			} else {
				fmt.Printf("@%s -> %s\n", strings.TrimPrefix(args[0], "@"), path)
			}
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:     "remove <name>",
		Aliases: []string{"rm", "del"},
		Short:   "删除别名",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			if !s.RemoveAlias(args[0]) {
				return fmt.Errorf("alias not found: %s", args[0])
			}
			if err := s.Save(); err != nil {
				return err
			}
			if !jsonOut {
				fmt.Printf("removed @%s\n", strings.TrimPrefix(args[0], "@"))
			}
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "列出别名",
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
		fmt.Printf("@%-16s %s\n", name, aliases[name])
	}
	return nil
}

// ---- import ----

func buildImportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "import <file>",
		Short: "从文本或常见历史文件导入路径",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			file, err := expandPath(args[0])
			if err != nil {
				return err
			}
			f, err := os.Open(file)
			if err != nil {
				return err
			}
			defer f.Close()

			s, err := loadStore()
			if err != nil {
				return err
			}
			imported := 0
			scanner := bufio.NewScanner(f)
			for scanner.Scan() {
				for _, path := range extractPaths(scanner.Text()) {
					if info, err := os.Stat(path); err == nil && info.IsDir() {
						s.AddManual(path, 1)
						imported++
					}
				}
			}
			if err := scanner.Err(); err != nil {
				return err
			}
			if err := s.Save(); err != nil {
				return err
			}
			if jsonOut {
				printJSON(map[string]any{"imported": imported, "file": file})
			} else {
				fmt.Printf("imported %d paths\n", imported)
			}
			return nil
		},
	}
}

// ---- completion / suggestions ----

func buildCompleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "complete [prefix]",
		Short:  "输出 shell 补全候选",
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
			for _, name := range sortedKeys(s.AllAliases()) {
				candidate := "@" + name
				if strings.HasPrefix(candidate, prefix) {
					fmt.Println(candidate)
					seen[candidate] = true
				}
			}
			for _, e := range s.All() {
				base := filepath.Base(e.Path)
				if base != "" && strings.HasPrefix(strings.ToLower(base), prefix) && !seen[base] {
					fmt.Println(base)
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
		Short:  "输出近似匹配建议",
		Hidden: true,
		Args:   cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			suggestions := matcher.Suggest(s, args, 5)
			for _, r := range suggestions {
				fmt.Println(r.Entry.Path)
			}
			return nil
		},
	}
}

// ---- version ----

func buildVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the jmp version",
		RunE: func(cmd *cobra.Command, args []string) error {
			if jsonOut {
				printJSON(map[string]any{"version": version})
				return nil
			}
			fmt.Println("jmp", version)
			return nil
		},
	}
}

// ---- stats ----

func buildStatsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stats",
		Short: "显示数据库统计信息",
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

			fmt.Printf("数据库路径:  %s\n", dbPath)
			fmt.Printf("路径条数:    %s\n", strconv.Itoa(len(entries)))
			fmt.Printf("总权重:      %.1f\n", totalWeight)
			fmt.Printf("总访问次数:  %d\n", totalVisits)
			if len(entries) > 0 {
				fmt.Printf("\nTop 10:\n")
				fmt.Printf("%-10s %s\n", "SCORE", "PATH")
				fmt.Println(strings.Repeat("-", 60))
				limit := 10
				if len(entries) < limit {
					limit = len(entries)
				}
				for _, e := range entries[:limit] {
					fmt.Printf("%-10.1f %s\n", e.Frecency(), e.Path)
				}
			}
			return nil
		},
	}
}

// ---- helpers ----

func loadStore() (*store.Store, error) {
	return store.Load(dbPath)
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func printSuggestions(w io.Writer, s *store.Store, terms []string) {
	results := matcher.Suggest(s, terms, 3)
	if len(results) == 0 {
		return
	}
	fmt.Fprintln(w, "suggestions:")
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
