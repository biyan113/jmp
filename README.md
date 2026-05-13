# jmp

智能目录跳转工具 — autojump 的 Go 重写增强版。

使用 frecency 算法（频率 + 时效衰减）排序，跨平台支持 macOS / Linux / WSL / Windows。

## 安装

```bash
# 从源码构建
make build
make install   # 安装到 /usr/local/bin/jmp
```

## Shell 集成

将以下内容添加到对应 shell 的配置文件中：

```bash
# bash (~/.bashrc)
eval "$(jmp init bash)"

# zsh (~/.zshrc)
eval "$(jmp init zsh)"

# fish (~/.config/fish/config.fish)
jmp init fish | source

# PowerShell ($PROFILE)
Invoke-Expression (& jmp init powershell | Out-String)
```

集成后即可使用 `j` 命令快速跳转。

## 使用方法

### 基本跳转

```bash
j foo          # 跳转到最匹配 "foo" 的目录
j foo bar      # 多关键词匹配
j @proj        # 通过别名跳转
j              # 打开 TUI 管理界面
```

### 路径管理

```bash
jmp add /path/to/dir          # 手动添加路径
jmp add /path/to/dir -w 10    # 指定初始权重
jmp remove /path/to/dir       # 删除路径记录（别名: rm, del）
jmp list                      # 列出所有路径
jmp list -n 20                # 最多显示 20 条
jmp list -q keyword           # 按关键词过滤
```

### 别名管理

```bash
jmp alias                     # 列出所有别名
jmp alias set proj /path      # 设置别名
jmp alias remove proj         # 删除别名
j @proj                       # 使用别名跳转
```

### TUI 界面

```bash
jmp tui       # 打开交互式管理界面
jmp --tui     # 同上
```

### 数据维护

```bash
jmp stats                     # 显示数据库统计
jmp doctor                    # 检查数据库健康状态
jmp clean                     # 删除不存在的路径和失效别名
jmp clean --dry-run           # 预览将删除的记录
```

### 数据导入

```bash
jmp import ~/path_history.txt   # 从文本文件导入路径
```

### 多设备同步

通过 SSH/SCP 跨设备同步数据库，合并策略为权重取最大值、访问次数取最大值、时间取最新。

```bash
jmp sync set user@host:~/.local/share/jmp/db.json        # 配置远程路径
jmp sync set user@host:~/.local/share/jmp/db.json 600    # 设置同步间隔（秒）
jmp sync                # 立即同步（pull + merge + push）
jmp sync push           # 仅推送
jmp sync pull           # 仅拉取并合并
jmp sync auto           # 自动同步（超过间隔才执行，供 shell hook 调用）
jmp sync status         # 查看同步状态
```

### 配置

```bash
jmp config                                  # 查看当前配置
jmp config color <auto|always|never|ansi256|truecolor>   # 设置颜色模式
```

### 全局选项

| 选项 | 说明 |
|------|------|
| `--db <path>` | 指定数据库文件路径 |
| `--json` | 以 JSON 格式输出 |

## 开发

```bash
make build      # 构建本地二进制
make test       # 运行测试
make lint       # 静态检查
make build-all  # 交叉编译所有平台
make clean      # 清理构建产物
```

## License

MIT
