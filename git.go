package main

// git.go —— 只读 git 调用层与输出解析。
//
// 安全约束（硬性）：本工具绝不执行 fetch / pull / checkout / reset / clean /
// prune / gc / add / commit / rm / mv / worktree add|remove|lock 等任何会改变
// 目标仓库的命令；只调用 rev-parse / worktree list / status / config --get /
// rev-list / log -1。并且所有 git 调用都带 --no-optional-locks
// （等价于 GIT_OPTIONAL_LOCKS=0），避免 git status 顺手刷新并回写 .git/index。

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

var (
	// gitBin 由 main 通过 exec.LookPath 填充（尊重 PATH，便于测试注入垫片）。
	gitBin = "git"
	// gitTimeout 为单次 git 调用超时，0 表示不限制。
	gitTimeout = 120 * time.Second
)

// gitEnvOverrides 锁死一切可能产生副作用或交互的隐式行为。
// 刻意不覆盖用户 locale / git config —— 这个工具要如实反映用户环境下的仓库
// 状态，只读性由 --no-optional-locks + 显式参数保证。
// 注意：同名变量必须先剔除再追加，否则子进程 getenv 取到的仍是旧值。
var gitEnvOverrides = [][2]string{
	{"GIT_OPTIONAL_LOCKS", "0"},  // 关键：status 不写 index
	{"GIT_TERMINAL_PROMPT", "0"}, // 绝不交互（永不 fetch，双保险）
	{"GIT_PAGER", "cat"},
}

func gitEnv() []string {
	env := os.Environ()
	out := make([]string, 0, len(env)+len(gitEnvOverrides))
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		skip := false
		for _, ov := range gitEnvOverrides {
			if key == ov[0] {
				skip = true
				break
			}
		}
		if !skip {
			out = append(out, kv)
		}
	}
	for _, ov := range gitEnvOverrides {
		out = append(out, ov[0]+"="+ov[1])
	}
	return out
}

type gitResult struct {
	stdout string
	stderr string
	code   int
}

// runGit 执行一次只读 git 命令。
func runGit(args ...string) gitResult {
	ctx := context.Background()
	cancel := func() {}
	if gitTimeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, gitTimeout)
	}
	defer cancel()

	full := make([]string, 0, len(args)+1)
	full = append(full, "--no-optional-locks")
	full = append(full, args...)

	cmd := exec.CommandContext(ctx, gitBin, full...)
	cmd.Env = gitEnv()
	cmd.Stdin = nil // 子进程读 /dev/null，绝不阻塞

	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()

	if ctx.Err() == context.DeadlineExceeded {
		// 合成失败结果，避免一个异常仓库拖死整轮巡检
		return gitResult{
			stderr: fmt.Sprintf("git 调用超时（>%s）: %s", gitTimeout, strings.Join(args, " ")),
			code:   124,
		}
	}

	code := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			code = -1
			fmt.Fprintf(&errb, "%v", err)
		}
	}
	return gitResult{stdout: out.String(), stderr: errb.String(), code: code}
}

// ---------------------------------------------------------------------------
// 解析 git 输出
// ---------------------------------------------------------------------------

type worktreeEntry struct {
	path     string
	head     string
	branch   string
	detached bool
	bare     bool
	locked   *string
	prunable *string
}

// parseWorktreePorcelain 解析 `git worktree list --porcelain`。
func parseWorktreePorcelain(text string) []worktreeEntry {
	var entries []worktreeEntry
	var cur *worktreeEntry
	flush := func() {
		if cur != nil {
			entries = append(entries, *cur)
			cur = nil
		}
	}
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, "\r")
		if line == "" {
			flush()
			continue
		}
		if cur == nil {
			cur = &worktreeEntry{}
		}
		switch {
		case strings.HasPrefix(line, "worktree "):
			cur.path = line[len("worktree "):]
		case strings.HasPrefix(line, "HEAD "):
			cur.head = line[len("HEAD "):]
		case strings.HasPrefix(line, "branch "):
			cur.branch = line[len("branch "):]
		case line == "detached":
			cur.detached = true
		case line == "bare":
			cur.bare = true
		case strings.HasPrefix(line, "locked"):
			s := strings.TrimSpace(line[len("locked"):])
			cur.locked = &s
		case strings.HasPrefix(line, "prunable"):
			s := strings.TrimSpace(line[len("prunable"):])
			cur.prunable = &s
		}
		// 其它未知行忽略（向前兼容）
	}
	flush()
	return entries
}

var conflictCodes = map[string]bool{
	"DD": true, "AU": true, "UD": true, "UA": true, "DU": true, "AA": true, "UU": true,
}

// parseStatusPorcelainZ 解析 `git status --porcelain=v1 -z`。
// 重命名/拷贝在 -z 下会多带一个原路径记录，必须跳过，否则会多算一条。
func parseStatusPorcelainZ(text string) (tracked, untracked, conflicts, ignored int) {
	tokens := strings.Split(text, "\x00")
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if tok == "" || len(tok) < 3 {
			continue
		}
		xy := tok[:2]
		switch {
		case xy == "??":
			untracked++
		case xy == "!!":
			ignored++
		default:
			if conflictCodes[xy] {
				conflicts++
			}
			tracked++
		}
		if strings.ContainsAny(xy, "RC") {
			i++ // 跳过原路径记录
		}
	}
	return
}

// parseLeftRightCount 解析 `git rev-list --left-right --count A...B`。
func parseLeftRightCount(s string) (left, right int, ok bool) {
	fields := strings.Fields(s)
	if len(fields) != 2 {
		return 0, 0, false
	}
	a, err1 := strconv.Atoi(fields[0])
	b, err2 := strconv.Atoi(fields[1])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return a, b, true
}

// allZeroSHA 表示未出生分支（unborn）的 HEAD。
const allZeroSHA = "0000000000000000000000000000000000000000"

// configGet 读取本地配置，未设置返回 nil。
func configGet(cwd, key string) *string {
	res := runGit("-C", cwd, "config", "--get", key)
	if res.code != 0 {
		return nil
	}
	v := strings.TrimSpace(res.stdout)
	if v == "" {
		return nil
	}
	return &v
}
