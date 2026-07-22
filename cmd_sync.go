package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bytenote/jmp/internal/store"
	"github.com/spf13/cobra"
)

// SyncConfig stores remote sync settings.
type SyncConfig struct {
	Remote   string `json:"remote"`
	Interval int    `json:"interval"`
}

func syncConfigFile() string {
	return filepath.Join(store.DataDir(), "sync.json")
}

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
		return nil, err
	}
	return &cfg, nil
}

func saveSyncConfig(cfg *SyncConfig) error {
	if err := os.MkdirAll(filepath.Dir(syncConfigFile()), 0o755); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	return os.WriteFile(syncConfigFile(), data, 0o644)
}

func lastSyncFile() string {
	return filepath.Join(store.DataDir(), ".last_sync")
}

func lastSyncTime() time.Time {
	data, err := os.ReadFile(lastSyncFile())
	if err != nil {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(data)))
	if err != nil {
		return time.Time{}
	}
	return t
}

func recordSync() error {
	if err := os.MkdirAll(store.DataDir(), 0o755); err != nil {
		return err
	}
	return os.WriteFile(lastSyncFile(), []byte(time.Now().Format(time.RFC3339)), 0o644)
}

// ---- commands ----

func buildSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "多设备数据库同步 (SCP)",
		Long: `通过 SSH/SCP 跨设备同步 jmp 数据库。

合并策略：权重取最大值、访问次数取最大值、时间取最新。

配置远程路径后，shell hook 会自动在后台同步。

示例：
  jmp sync set pan@server:~/.local/share/jmp/db.json
  jmp sync set pan@server:~/.local/share/jmp/db.json 600
  jmp sync          # 立即同步（pull + push）
  jmp sync push     # 仅推送
  jmp sync pull     # 仅拉取并合并`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSyncNow()
		},
	}

	cmd.AddCommand(
		buildSyncSetCmd(),
		buildSyncPushCmd(),
		buildSyncPullCmd(),
		buildSyncAutoCmd(),
		buildSyncStatusCmd(),
	)

	return cmd
}

func buildSyncSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <remote> [interval]",
		Short: "设置远程路径和同步间隔",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			interval := 300
			if len(args) == 2 {
				n, err := strconv.Atoi(args[1])
				if err != nil || n < 60 {
					return fmt.Errorf("间隔必须 >= 60 秒")
				}
				interval = n
			}
			cfg := &SyncConfig{Remote: args[0], Interval: interval}
			if err := saveSyncConfig(cfg); err != nil {
				return err
			}
			if jsonOut {
				printJSON(cfg)
			} else {
				fmt.Printf("已配置同步\n  远程: %s\n  间隔: %ds\n", cfg.Remote, cfg.Interval)
			}
			return nil
		},
	}
}

func buildSyncPushCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "push",
		Short: "推送本地数据库到远程",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := requireSyncConfig()
			if err != nil {
				return err
			}
			c := exec.Command("scp", "-q", dbPath, cfg.Remote)
			out, err := c.CombinedOutput()
			if err != nil {
				return fmt.Errorf("scp 失败: %s", out)
			}
			if !jsonOut {
				fmt.Println("已推送")
			}
			return recordSync()
		},
	}
}

func buildSyncPullCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pull",
		Short: "拉取远程数据库并合并",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := requireSyncConfig()
			if err != nil {
				return err
			}
			tmp, err := os.CreateTemp("", "jmp_pull_*.json")
			if err != nil {
				return fmt.Errorf("创建临时文件失败: %w", err)
			}
			tmpName := tmp.Name()
			tmp.Close()
			defer os.Remove(tmpName)

			c := exec.Command("scp", "-q", cfg.Remote, tmpName)
			out, err := c.CombinedOutput()
			if err != nil {
				return fmt.Errorf("scp 拉取失败: %s", out)
			}

			local, err := store.Load(dbPath)
			if err != nil {
				return err
			}
			remote, err := store.Load(tmpName)
			if err != nil {
				return err
			}
			local.MergeFrom(remote)
			if err := local.Save(); err != nil {
				return err
			}
			if !jsonOut {
				fmt.Println("已拉取并合并")
			}
			return recordSync()
		},
	}
}

func buildSyncAutoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "auto",
		Short: "自动同步（超过间隔才执行，供 shell hook 调用）",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadSyncConfig()
			if err != nil || cfg == nil {
				return nil
			}
			last := lastSyncTime()
			if !last.IsZero() && time.Since(last) < time.Duration(cfg.Interval)*time.Second {
				return nil
			}
			_ = runSyncNow()
			return nil
		},
	}
}

func buildSyncStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "显示同步配置和状态",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadSyncConfig()
			if err != nil {
				return err
			}
			if cfg == nil {
				fmt.Println("同步未配置，运行: jmp sync set <remote>")
				return nil
			}
			last := lastSyncTime()
			lastStr := "从未"
			if !last.IsZero() {
				lastStr = last.Format("2006-01-02 15:04:05")
			}
			if jsonOut {
				printJSON(map[string]any{
					"remote":    cfg.Remote,
					"interval":  cfg.Interval,
					"last_sync": lastStr,
					"local_db":  dbPath,
				})
				return nil
			}
			fmt.Printf("远程:     %s\n", cfg.Remote)
			fmt.Printf("间隔:     %ds\n", cfg.Interval)
			fmt.Printf("上次同步: %s\n", lastStr)
			fmt.Printf("本地 DB:  %s\n", dbPath)
			return nil
		},
	}
}

func runSyncNow() error {
	cfg, err := requireSyncConfig()
	if err != nil {
		return err
	}
	// Pull first: if the pull or merge fails, abort BEFORE pushing so we never
	// overwrite the remote with a stale/empty local copy.
	tmp, err := os.CreateTemp("", "jmp_pull_*.json")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpName := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpName)

	if c := exec.Command("scp", "-q", cfg.Remote, tmpName); c.Run() == nil {
		local, errL := store.Load(dbPath)
		remote, errR := store.Load(tmpName)
		if errL != nil {
			return fmt.Errorf("加载本地数据库失败: %w", errL)
		}
		if errR != nil {
			return fmt.Errorf("加载远程数据库失败: %w", errR)
		}
		local.MergeFrom(remote)
		if err := local.Save(); err != nil {
			return fmt.Errorf("保存合并结果失败: %w", err)
		}
	} else {
		// Pull failed (remote unreachable / first sync). That's acceptable, but
		// we still push local state so the remote gets seeded. A load error
		// below will abort as above.
		local, errL := store.Load(dbPath)
		if errL != nil {
			return fmt.Errorf("加载本地数据库失败: %w", errL)
		}
		_ = local // local load is only a sanity check here before the push
	}
	// Push merged result
	c := exec.Command("scp", "-q", dbPath, cfg.Remote)
	if out, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("scp push 失败: %s", out)
	}
	if !jsonOut {
		fmt.Println("已同步")
	}
	return recordSync()
}

func requireSyncConfig() (*SyncConfig, error) {
	cfg, err := loadSyncConfig()
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, fmt.Errorf("同步未配置，运行: jmp sync set <remote>")
	}
	return cfg, nil
}
