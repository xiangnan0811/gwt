# gwt — 只读的多仓库 Git worktree 终端巡检工具

给自己用的最小工具：给一个或多个仓库路径，集中列出这些仓库的**全部 worktree 实况**。
只展示，不管理、不清理、不修复。

- **Go 实现，编译成单个静态二进制**（约 2.9 MB，`CGO_ENABLED=0`，无任何运行时依赖，
  不依赖 Python/解释器）
- **纯标准库**，`go.mod` 里没有任何第三方依赖（东亚列宽表是内嵌的区间表）
- **严格只读**：不 fetch / pull / checkout / reset / clean / prune / gc / add /
  commit / rm / mv / worktree add|remove|lock / update-ref …，全部 git 调用都带
  `--no-optional-locks`（`GIT_OPTIONAL_LOCKS=0`），连 `git status` 顺手回写
  `.git/index` 的行为都禁掉
- ahead/behind **只基于本地已有引用**计算，绝不联网

## 构建

```bash
cd ~/code/gwt
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o gwt .   # 产出静态二进制 ./gwt
file gwt        # → ELF 64-bit LSB executable, statically linked, stripped
go test ./...   # 21 个 Go 单元测试
```

只用到标准库，无需联网拉依赖。想装到 PATH 就自己 `install -m755 gwt ~/.local/bin/`。

## 运行

```bash
./gwt ~/code/repo-a ~/code/repo-b        # 默认终端表格
./gwt --json ~/code/repo-a | jq .        # JSON
./gwt --strict ~/code/repo-a             # 有异常时退出码 1
./gwt ~/code/repo-a --json               # 选项与路径可以交错
```

路径可以是：仓库主工作树、仓库内任意子目录、或任意一个 linked worktree 的路径
（会归到包含它的那个仓库，遵循 git 语义）。

### 参数

| 参数 | 说明 |
| --- | --- |
| `paths...` | 一个或多个仓库路径（必填） |
| `--json` | 输出 JSON（`SetEscapeHTML(false)`，字段见下） |
| `--width N` | 表格总宽度，默认取终端宽度（COLUMNS → ioctl → 120） |
| `--no-truncate` | 不按宽度截断（可能很宽，适合重定向到文件） |
| `--untracked {all,normal}` | 未跟踪文件统计方式：`all`（默认）展开未跟踪目录逐个计数，`normal` 把整个未跟踪目录算 1 条 |
| `--color {auto,always,never}` | 着色策略，默认 `auto`（仅真 tty；用 TCGETS 判定，不会把 /dev/null 当成终端） |
| `--timeout SEC` | 单次 git 调用超时，默认 120 秒，`0` 表示不限制 |
| `--strict` | 存在需关注项时以退出码 1 结束 |
| `--version` | 版本 |
| `-h, --help` | 帮助 |

### 退出码

| 码 | 含义 |
| --- | --- |
| 0 | 所有输入路径都成功解析（worktree 自身的问题不算失败，除非 `--strict`） |
| 1 | 有输入路径无法解析（不存在 / 不是仓库 / 是文件），或 `--strict` 下存在需关注项 |
| 2 | 用法错误，或找不到 `git` |

## 表格列

| 列 | 含义 / 异常也照样写清楚 |
| --- | --- |
| 仓库 | 真实仓库名（主工作树目录名；bare 仓库去掉 `.git` 后缀），主工作树带 `[main]` |
| worktree 路径 | 完整路径；过窄时从左截断（保留末尾，更有辨识度） |
| 分支 | 正常分支名；`(detached) <sha>`；`(unborn) <branch>`；`(bare)`；`(未知 HEAD)` |
| 改动 | 已跟踪文件的改动条数（含暂存、未暂存、冲突；重命名/拷贝各计 1）；路径失效时是 `-` 而不是 0 |
| 未跟踪 | 未跟踪文件数（不含 ignored）；路径失效时是 `-` |
| upstream | `origin/main` 这类名字；`(无)` 表示没配 upstream；`<name> (本地无此引用)` 表示配了但本地没这个 ref；`-` 表示不适用（detached/bare） |
| ahead/behind | `+ahead/-behind`，如 `+1/-2`；`-` 不适用；`?` 无法计算（路径失效或本地缺该上游引用） |
| 标记 | 只列其它列看不出来的问题：`路径失效` `待清理` `已锁定` `冲突` `路径异常` `读取失败` `HEAD异常`（`detached`/`无提交`/`bare`/`无up`/`缺up引用` 已在别的列里写明，不重复占位） |

表格下方有汇总行、截断提示和图例。

## JSON 结构

```jsonc
{
  "schema": 1, "tool": "gwt", "version": "0.2.0",
  "generated_at": "2026-09-16T21:40:00+08:00",
  "read_only": true, "fetched": false, "untracked_mode": "all",
  "notes": ["..."],
  "summary": { "repos": 1, "worktrees": 4, "dirty_worktrees": 2,
               "worktrees_with_problems": 3, "failed_inputs": 0 },
  "repos": [
    { "input_path": "...", "repo_name": "alpha", "repo_root": "...",
      "common_dir": "...", "worktree_count": 4,
      "worktrees": [
        { "path": "...", "is_main_worktree": true, "is_bare": false,
          "path_exists": true, "locked": null, "prunable": null,
          "head": { "state": "branch|detached|unborn|unknown", "branch": "main",
                    "branch_ref": "refs/heads/main", "commit": "...", "short_commit": "..." },
          "tracked_modified": 3, "untracked": 4, "conflicts": 0,
          "upstream": { "state": "ok|none|missing_ref|not_applicable|unknown",
                        "name": "origin/main", "ref": "refs/remotes/origin/main",
                        "ahead": 1, "behind": 2, "based_on": "local-ref-no-fetch",
                        "ref_commit": "...", "ref_commit_date": "..." },
          "problems": ["detached"], "problem_labels": ["detached HEAD"], "errors": [] }
      ] }
  ],
  "errors": [ { "input_path": "/nope", "error": "路径不存在: /nope", "kind": "error" },
              { "input_path": "...", "kind": "duplicate", "error": "...", "duplicate_of": "..." } ]
}
```

关键约定：**未知就是未知**。拿不到的数一律 `null` / `"unknown"`，不会被填成 0 或
当成正常状态。`upstream.state` 的取值把「没配 upstream」(`none`) 和「配了但本地没有
该 ref」(`missing_ref`) 严格区分开。

## 已知限制

1. **ahead/behind 可能滞后于远端**：只看本地引用，不 fetch。远端有新提交时数字不会变
   （这是刻意的：不联网、不改仓库）。`upstream.ref_commit_date` 可以帮你判断本地引用有多旧。
2. **不反映远端分支是否已被删除**：本地没有对应 remote-tracking ref 时只标
   `本地缺 upstream 引用`，不会去问远端。
3. **`--untracked all` 在超大仓库上可能慢**：它要展开所有未跟踪目录逐个计数；
   用 `--untracked normal` 可以退化成「未跟踪目录算 1 条」。
4. **只统计文件条数，不给文件列表**：`改动`/`未跟踪` 是计数，具体是哪些文件请用
   `git status`。也**不**统计 stash、不统计 submodule 内部改动、不含 ignored 文件。
5. **`tracked_modified` 把冲突也算进去**，并额外用 `conflicts` 字段和 `冲突` 标记提示；
   rebase/merge 中间的中间态不会被专门识别。
6. **路径失效的 worktree 只展示注册信息**：目录已被删掉的 worktree，改动/未跟踪/upstream
   都拿不到，统一显示 `-`、`?`，并在 `标记` 里写 `路径失效` / `待清理`。工具不会替你 prune。
7. **同一仓库重复传入会被去重**，只按 common dir 判定，附一条 `kind: "duplicate"` 说明。
8. 传入仓库内的子目录时，展示的是**包含它的那个仓库**的全部 worktree（git 语义）。
   典型坑：`~/.nvm` 之类被 git 管理的目录里，任何子目录都会归到该仓库。
9. **终端窄于 82 列时表格会溢出**：8 列各自的最小宽度之和是 68，加上 7 个分隔符
   （每个 2 列）共 **82** 列 —— 窄于此时已经压无可压；工具不会把列压到最小值以下
   （宁可溢出成 82 列，也不给出残缺的列），并会打印截断提示。实测：宽度 ≥ 82 一定能
   放下，≤ 81 时表格固定在 82 列。此时建议 `--no-truncate` 重定向到文件，或换个宽一点的
   终端。表格下方的图例/提示已控制在 77 列以内，不会在 80 列终端上折行。
10. 不解析 git 的复杂配置语义（如 `includeIf`），一律以 `git config --get` 的结果为准；
    也不覆盖用户的 locale / git config —— 工具要如实反映你环境下的仓库状态。
11. **Linux 专用**：是否为 tty、终端宽度走的是 `syscall` + `TCGETS`/`TIOCGWINSZ`
    ioctl。macOS/Windows 未测试、未适配（macOS 换个 ioctl 常量即可，但没验证过）。

## 测试

两层，共 53 个测试（21 个 Go 单元测试 + 32 个 Python 黑盒验收）。

**Go 单元测试（21 个，随实现走）**

```bash
go test ./... -v
```

覆盖 `git.go` 的解析器（`git worktree list --porcelain` 含 bare/locked/prunable/含空格
路径、`status --porcelain -z` 的重命名/拷贝/冲突/ignored、`rev-list --left-right --count`、
`shaPtr` 对 unborn 全零 SHA 的处理）与 `table.go` 的显示宽度（中文/日文/韩文/全角/emoji/
组合符/零宽）、左右截断、`pad`、以及列宽收缩策略（窄终端下优先牺牲 upstream/标记而不是
路径；`--no-truncate` 时列宽等于内容宽度；以及把「82 列硬最小值下界」这个
文档数字钉住的测试）。

**Python 黑盒验收（32 个，只通过 CLI 观察行为）**

```bash
go build -o gwt .                              # 先编译出被测二进制
python3 -m unittest discover -s tests -t . -v  # 再跑验收
GWT_BIN=/path/to/gwt python3 tests/test_gwt.py # 也可指定别的二进制
```

覆盖：正常分支 / 脏工作区（改动+暂存+删除+未跟踪文件+未跟踪目录）/ 无 upstream /
upstream 配了但本地缺 ref / 本地分支作 upstream（`remote = .`）/ detached HEAD /
路径含空格 / 中文仓库名与分支名 / 路径失效(prunable) / bare + 其 linked worktree /
unborn / locked / 真实 merge 冲突 / 已暂存重命名只计 1 / 子目录与 worktree 作输入 /
相对路径 / 去重 / 退出码 / 窄宽不崩 / JSON 字段契约 / 未跟踪两种模式。

只读性由 4 个测试守住，并且**这些测试本身用变异测试验证过有效**（把实现改成会写
index / 会 fetch 的版本后必须失败，否则测试就是摆设）：

- 运行前后对整个仓库目录做 `(size, mtime_ns, sha256)` 快照比对，包含 `.git/index`；
  测试前会先 `utime` 把 index 的 stat 缓存弄脏，这样普通 `git status` 一定会回写 index
- 用 `git` PATH 垫片记录**每一条** git 调用（按 argv 用 `\x1f` 精确切分，路径带空格
  也不会误判），断言子命令落在只读白名单内、每条都带 `--no-optional-locks`
- 断言 worktree 注册表（`git worktree list --porcelain`）跑前跑后完全一致
- 断言不产生 `.git/index.lock` 等新文件

`tests/fixtures.py` 会在临时目录里造出上述全部场景（含一个真 bare 仓库当 remote、
一次真实 `git fetch` 用于制造 ahead/behind），可以单独用来手工验证：

```bash
python3 tests/fixtures.py /tmp/gwt-demo      # 打印各 fixture 路径
./gwt /tmp/gwt-demo/alpha /tmp/gwt-demo/beta /tmp/gwt-demo/delta
```

### 关于实现历史

这个工具最初是单文件 Python 实现，后来整体移植成 Go（JSON 契约、表格渲染、异常
语义保持一致）。移植时用 Python 版的输出当黄金样本做了全量交叉校验（6 个 fixture
仓库 × 多种宽度/模式的表格 + JSON），确认一致后才删掉 Python 版。
留下的 `tests/` 是纯黑盒验收：它只调用 CLI、解析输出、比对仓库快照，因此换实现
不影响它 —— 这既是当时的验收门槛，也是以后回归的保障。
# gwt
