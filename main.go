package main

// main.go —— CLI 入口：参数解析、巡检编排、输出与退出码。

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const prog = "gwt"

// version 刻意用 var：打包（PKGBUILD）与 CI 都用
// -ldflags "-X main.version=$pkgver" 注入真实版本号，普通 go build 显示下面的默认值。
var version = "0.3.0"

type options struct {
	json        bool
	width       int
	widthSet    bool
	noTruncate  bool
	untracked   string
	color       string
	timeout     float64
	strict      bool
	showHelp    bool
	showVersion bool
	paths       []string
}

func usage() string {
	return `用法: gwt [选项] REPO_PATH [REPO_PATH ...]

只读的多仓库 Git worktree 终端巡检工具（不 fetch、不修改任何仓库）。
路径可以是主工作树、仓库内任意子目录、或任意一个 linked worktree。

选项:
  --json                  输出 JSON（便于脚本消费）
  --width N               表格总宽度，默认取终端宽度
  --no-truncate           不按终端宽度截断（可能很宽）
  --untracked all|normal  未跟踪文件统计方式，all=展开目录逐个计数（默认 all）
  --color auto|always|never  着色策略，默认 auto（仅 tty）
  --timeout SEC           单次 git 调用超时秒数，0 表示不限制（默认 120）
  --strict                存在需要关注的问题时以退出码 1 结束
  --version               显示版本
  -h, --help              显示本帮助

退出码:
  0  全部输入路径解析成功（worktree 自身的问题不算失败，除非 --strict）
  1  有输入路径无法解析，或 --strict 下存在需关注项
  2  用法错误，或找不到 git

示例:
  gwt ~/code/a ~/code/b
  gwt --json ~/code/a | jq .summary
  gwt --strict ~/code/a
`
}

// parseArgs 手写解析，支持「选项与路径交错」（argparse 风格）：
//
//	gwt ~/repo --json
func parseArgs(argv []string) (*options, error) {
	o := &options{untracked: "all", color: "auto", timeout: 120}
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		if a == "--" {
			o.paths = append(o.paths, argv[i+1:]...)
			break
		}
		if a == "-" || !strings.HasPrefix(a, "-") {
			o.paths = append(o.paths, a)
			continue
		}
		name := strings.TrimLeft(a, "-")
		val := ""
		hasVal := false
		if idx := strings.Index(name, "="); idx >= 0 {
			val, name, hasVal = name[idx+1:], name[:idx], true
		}
		needVal := func() error {
			if hasVal {
				return nil
			}
			if i+1 >= len(argv) {
				return fmt.Errorf("参数 --%s 缺少取值", name)
			}
			i++
			val = argv[i]
			return nil
		}
		switch name {
		case "json":
			o.json = true
		case "no-truncate":
			o.noTruncate = true
		case "strict":
			o.strict = true
		case "help", "h":
			o.showHelp = true
		case "version":
			o.showVersion = true
		case "width", "untracked", "color", "timeout":
			if err := needVal(); err != nil {
				return nil, err
			}
			switch name {
			case "width":
				n, convErr := strconv.Atoi(val)
				if convErr != nil || n <= 0 {
					return nil, fmt.Errorf("--width 需要一个正整数，收到 %q", val)
				}
				o.width, o.widthSet = n, true
			case "untracked":
				if val != "all" && val != "normal" {
					return nil, fmt.Errorf("--untracked 只支持 all 或 normal，收到 %q", val)
				}
				o.untracked = val
			case "color":
				if val != "auto" && val != "always" && val != "never" {
					return nil, fmt.Errorf("--color 只支持 auto/always/never，收到 %q", val)
				}
				o.color = val
			case "timeout":
				f, convErr := strconv.ParseFloat(val, 64)
				if convErr != nil || f < 0 {
					return nil, fmt.Errorf("--timeout 需要非负秒数，收到 %q", val)
				}
				o.timeout = f
			}
		default:
			return nil, fmt.Errorf("未知参数: %s", a)
		}
	}
	return o, nil
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(argv []string) int {
	opts, err := parseArgs(argv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n\n", prog, err)
		fmt.Fprint(os.Stderr, usage())
		return 2
	}
	if opts.showHelp {
		fmt.Print(usage())
		return 0
	}
	if opts.showVersion {
		fmt.Printf("%s %s\n", prog, version)
		return 0
	}
	if len(opts.paths) == 0 {
		fmt.Fprintf(os.Stderr, "%s: 至少需要一个仓库路径\n\n", prog)
		fmt.Fprint(os.Stderr, usage())
		return 2
	}

	bin, lookErr := exec.LookPath("git")
	if lookErr != nil {
		fmt.Fprintf(os.Stderr, "%s: 找不到 git 可执行文件，请先安装 git\n", prog)
		return 2
	}
	gitBin = bin
	gitTimeout = time.Duration(opts.timeout * float64(time.Second))

	var repos []*Repo
	errs := []jsonError{}
	seen := map[string]string{}
	for _, rawPath := range opts.paths {
		repo, errMsg := inspectRepo(rawPath, opts.untracked)
		if errMsg != "" {
			errs = append(errs, jsonError{InputPath: rawPath, Error: errMsg, Kind: "error"})
			continue
		}
		if prev, dup := seen[repo.commonDir]; dup {
			errs = append(errs, jsonError{
				InputPath:   rawPath,
				Kind:        "duplicate",
				Error:       "与 " + prev + " 指向同一个仓库，已跳过重复",
				DuplicateOf: prev,
			})
			continue
		}
		seen[repo.commonDir] = rawPath
		repos = append(repos, repo)
	}

	doc := buildDocument(repos, errs, opts.untracked)

	if opts.json {
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(doc); err != nil {
			fmt.Fprintf(os.Stderr, "%s: 写出 JSON 失败: %v\n", prog, err)
			return 2
		}
	} else {
		pal := palette{}
		switch opts.color {
		case "always":
			pal.enabled = true
		case "never":
			pal.enabled = false
		default:
			pal.enabled = isTerminal(os.Stdout)
		}

		for _, e := range errs {
			if e.Kind == "duplicate" {
				fmt.Fprintln(os.Stderr, pal.dim("提示: "+e.InputPath+": "+e.Error))
			} else {
				fmt.Fprintln(os.Stderr, pal.red("错误: "+e.InputPath+": "+e.Error))
			}
		}

		if len(repos) > 0 {
			w := opts.width
			if !opts.widthSet {
				w = termWidth()
			}
			lines, truncated := renderTable(repos, w, opts.noTruncate, pal)
			for _, line := range lines {
				fmt.Println(line)
			}
			fmt.Println()
			s := doc.Summary
			fmt.Println(pal.bold(fmt.Sprintf(
				"共 %d 个仓库 / %d 个 worktree；有未提交改动 %d 个；需关注 %d 个",
				s.Repos, s.Worktrees, s.DirtyWorktrees, s.WorktreesWithProblems)))
			if truncated {
				fmt.Println(pal.yellow(fmt.Sprintf(
					"（当前宽度 %d 列，部分列已截断；用 --no-truncate 或 --width N 查看完整内容）", w)))
			}
			fmt.Println(pal.dim(
				"图例: 改动=已跟踪文件修改数(含暂存/冲突, 重命名计 1)\n" +
					"      未跟踪=未跟踪文件数(不含 ignored)\n" +
					"      ahead/behind 形如 +a/-b，只按本地引用算，不 fetch\n" +
					"      - = 无 upstream / 不适用\n" +
					"      ? = 无法计算（路径失效，或本地缺该 upstream 引用）"))
		} else {
			fmt.Fprintln(os.Stderr, pal.red("没有可展示的仓库。"))
		}
	}

	if doc.Summary.FailedInputs > 0 {
		return 1
	}
	if opts.strict && doc.Summary.WorktreesWithProblems > 0 {
		return 1
	}
	return 0
}
