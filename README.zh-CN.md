# jmp

[English](README.md) | [简体中文](README.zh-CN.md)

**jmp** 是一个智能目录跳转工具 ——
[autojump](https://github.com/wting/autojump) 的 Go 重写增强版。它会学习你
`cd` 进入过的目录，让你用几个按键就能跳回去。

排序采用 **frecency** 算法（频率 + 时效衰减），子串匹配优先于模糊匹配。
跨平台支持：macOS / Linux / WSL / Windows。

---

## 特性

- 🚀 **一键跳转** —— `j foo` 跳转到最常使用、匹配 `foo` 的目录。
- 🧠 **frecency 排序** —— 结合访问频率和 7 天半衰期的时效衰减，近期、
  高频使用的目录优先。
- 🔍 **子串 + 模糊匹配** —— 子串精确命中永远排在模糊匹配之上；多关键词
  缩小结果范围。
- 🏷️ **别名** —— `jmp alias set proj /code/project`，然后用 `j @proj` 跳转。
- 🖥️ **TUI 管理** —— 交互式界面（Bubble Tea），可搜索、星标、分类、
  编辑权重、删除记录。
- 🐚 **Shell 集成** —— 支持 bash、zsh、fish、PowerShell，带 Tab 补全和
  `back` / `fwd` / `-` 导航历史。
- 🔄 **多设备同步** —— 通过 SSH/SCP 跨设备同步数据库（pull → merge → push），
  可配置同步间隔。
- 📥 **导入** —— 从文本文件或常见历史文件导入路径。
- 🧰 **维护工具** —— `doctor`、`clean`、`stats`、JSON 输出等。

<!-- TUI 截图占位。请把截图放到 docs/images/tui-main.png（在 80x24 以上的
     终端里运行 `jmp tui`，然后截图）。图片放进去后即可在此处显示。 -->
<p align="center">
  <img src="./docs/images/tui-main.png" alt="jmp TUI 管理界面" width="720">
</p>

## 安装

```bash
# 方式 1: go install (需要 Go 1.21+)
go install github.com/bytenote/jmp@latest

# 方式 2: 从源码构建
make build
make install   # 安装到 /usr/local/bin/jmp

# 方式 3: 从 Releases 下载预编译二进制
# https://github.com/biyan113/jmp/releases
```

需要 Go 1.21+（使用了内置的 `min`/`max`）。

## Shell 集成

将对应行加入 shell 配置文件，然后重启 shell，即可使用 `j` 命令。

```bash
# bash  (~/.bashrc)
eval "$(jmp init bash)"

# zsh   (~/.zshrc)
eval "$(jmp init zsh)"

# fish  (~/.config/fish/config.fish)
jmp init fish | source

# PowerShell  ($PROFILE)
Invoke-Expression (& jmp init powershell | Out-String)
```

集成脚本会自动记录你进入的每个目录，并在配置了同步时后台同步。

## 使用方法

### 基本跳转

```bash
j foo          # 跳转到最匹配 "foo" 的目录
j foo bar      # 多关键词匹配（必须全部命中）
j @proj        # 通过别名跳转
j              # 打开 TUI 管理界面
j -            # 返回上一个目录
j back         # 向后跳（需要 JMP_BACK，由集成脚本设置）
j fwd          # 向前跳（需要 JMP_FWD）
j /etc         # 绝对路径
j ~/Documents  # 家目录相对路径
j src          # 相对子目录（数据库无匹配时）
```

**`j` 同时具备 `cd` 能力**：先查 jmp 数据库，未命中时把参数当作目录路径
`cd` 过去（支持 `~`、绝对路径、相对名）。所以 `j src` 在有记录时跳老项目，
没记录时进入当前 `src` 子目录。这样 `cd` 进入的目录也会被 shell hook 记录，
下次就能直接 `j` 到。

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
jmp alias remove proj         # 删除别名（别名: rm, del）
j @proj                       # 使用别名跳转
```

### TUI 界面

```bash
jmp tui       # 打开交互式管理界面
jmp --tui     # 同上
```

按键：`↑/↓` 移动 · `enter` 跳转 · `/` 搜索 · `space` 标记 ·
`s` 星标 · `c` 分类 · `e` 编辑权重 · `d` 删除 · `a` 添加 ·
`1/2/3/4` 过滤（全部/最近/星标/已分类）· `i` 详情 ·
`p` 预览 · `?` 帮助 · `q` 退出。

### 数据维护

```bash
jmp stats                     # 显示数据库统计
jmp doctor                    # 检查数据库健康状态
jmp clean                     # 删除不存在的路径和失效别名
jmp clean --dry-run           # 预览将删除的记录
```

### 数据导入

```bash
jmp import ~/path_history.txt   # 从文本/历史文件导入路径
```

### 多设备同步

通过 SSH/SCP 跨设备同步数据库。合并策略：**权重取最大、访问次数取最大、
时间取最新**。配置后，shell hook 会按间隔在后台同步。

```bash
jmp sync set user@host:~/.local/share/jmp/db.json        # 配置远程路径
jmp sync set user@host:~/.local/share/jmp/db.json 600    # 带同步间隔（秒）
jmp sync                # 立即同步（pull + merge + push）
jmp sync push           # 仅推送
jmp sync pull           # 仅拉取并合并
jmp sync auto           # 超过间隔才执行（供 shell hook 调用）
jmp sync status         # 查看配置和上次同步时间
```

> 需要对远程主机配置免密 SSH/SCP 访问（如密钥认证）。

### 配置

```bash
jmp config                                  # 查看当前配置
jmp config color <auto|always|never|ansi256|truecolor>   # 设置 TUI 颜色模式
```

### 全局选项

| 选项 | 说明 |
|------|------|
| `--db <path>` | 指定数据库文件路径 |
| `--json`     | 以 JSON 格式输出 |

```bash
jmp version    # 输出版本号
```

## 开发

```bash
make build      # 构建本地二进制
make test       # 运行测试
make lint       # 静态检查
make build-all  # 交叉编译所有平台到 dist/
make clean      # 清理构建产物
```

## 排序原理

每个条目的 frecency 分数为：

```
frecency = weight × 0.5 ^ (距上次访问的小时数 / 168)
```

7 天半衰期意味着一周前访问的目录价值只有现在访问的一半。当所有条目总权重
超过 9000 时，每个权重乘以 0.9（autojump 式的衰减），防止无限增长。

在 frecency 之上，`matcher.Rank` 还会叠加加成/惩罚：路径末尾组件精确
匹配（×3）、前缀匹配（×1.5）、星标（×1.25）、别名匹配（×2）、
当前目录邻近（×1.2）、路径不存在（×0.1）。子串匹配永远排在模糊匹配之上。

## License

MIT
