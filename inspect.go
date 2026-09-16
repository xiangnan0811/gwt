package main

// inspect.go —— 数据模型、巡检逻辑、JSON 契约。

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

var problemLabels = map[string]string{
	"path_missing":         "路径失效",
	"not_a_worktree":       "路径异常",
	"prunable":             "可清理(prunable)",
	"locked":               "已锁定",
	"detached":             "detached HEAD",
	"unborn":               "无提交(unborn)",
	"no_upstream":          "无 upstream",
	"upstream_ref_missing": "本地缺 upstream 引用",
	"conflicts":            "有冲突",
	"bare":                 "bare 仓库",
	"status_error":         "状态读取失败",
	"head_unknown":         "HEAD 未知",
}

// problemShort 表格里用的紧凑标签（JSON 里仍给完整标签）。
var problemShort = map[string]string{
	"path_missing":         "路径失效",
	"not_a_worktree":       "路径异常",
	"prunable":             "待清理",
	"locked":               "已锁定",
	"detached":             "detached",
	"unborn":               "无提交",
	"no_upstream":          "无up",
	"upstream_ref_missing": "缺up引用",
	"conflicts":            "冲突",
	"bare":                 "bare",
	"status_error":         "读取失败",
	"head_unknown":         "HEAD异常",
}

// problemRedundantInTable：这些状态在 分支/upstream 列已经写清楚了，不再重复占位。
var problemRedundantInTable = map[string]bool{
	"detached": true, "unborn": true, "bare": true,
	"no_upstream": true, "upstream_ref_missing": true,
}

// severeProblems 需要以“错误”颜色高亮的问题。
var severeProblems = map[string]bool{
	"path_missing": true, "not_a_worktree": true,
	"status_error": true, "head_unknown": true,
}

// ---------------------------------------------------------------------------
// 数据结构
// ---------------------------------------------------------------------------

// Head 的 State 取值：branch | detached | unborn | unknown
type Head struct {
	State     string
	Branch    *string
	BranchRef *string
	Commit    *string
}

func (h *Head) shortCommit() *string {
	if h.Commit == nil || *h.Commit == "" {
		return nil
	}
	s := *h.Commit
	if len(s) > 12 {
		s = s[:12]
	}
	return &s
}

// Upstream 的 State 取值：none | ok | missing_ref | not_applicable | unknown
type Upstream struct {
	State     string
	Name      *string
	Ref       *string
	Ahead     *int
	Behind    *int
	RefCommit *string
	RefDate   *string
}

type Worktree struct {
	repoName   string
	repoRoot   *string
	commonDir  string
	path       string
	isMain     bool
	bare       bool
	locked     *string
	prunable   *string
	pathExists bool
	head       Head
	tracked    *int
	untracked  *int
	conflicts  *int
	upstream   Upstream
	problems   []string
	errs       []string
}

func (w *Worktree) addProblem(code string) {
	for _, p := range w.problems {
		if p == code {
			return
		}
	}
	w.problems = append(w.problems, code)
}

func (w *Worktree) branchDisplay() string {
	switch {
	case w.bare:
		return "(bare)"
	case w.head.State == "branch":
		if w.head.Branch == nil {
			return "(未知分支)"
		}
		return *w.head.Branch
	case w.head.State == "detached":
		sha := w.head.shortCommit()
		if sha == nil {
			return "(detached) ?"
		}
		return "(detached) " + *sha
	case w.head.State == "unborn":
		s := "(unborn)"
		if w.head.Branch != nil {
			s += " " + *w.head.Branch
		}
		return s
	default:
		return "(未知 HEAD)"
	}
}

func (w *Worktree) upstreamDisplay() string {
	switch w.upstream.State {
	case "none":
		return "(无)"
	case "not_applicable":
		return "-"
	case "unknown":
		return "(未知)"
	case "missing_ref":
		name := "?"
		if w.upstream.Name != nil {
			name = *w.upstream.Name
		}
		return name + " (本地无此引用)"
	default:
		if w.upstream.Name != nil {
			return *w.upstream.Name
		}
		return "?"
	}
}

func (w *Worktree) aheadBehindDisplay() string {
	switch w.upstream.State {
	case "none", "not_applicable":
		return "-"
	case "unknown":
		return "?"
	}
	if w.upstream.Ahead == nil || w.upstream.Behind == nil {
		return "?"
	}
	plus := "0"
	if *w.upstream.Ahead != 0 {
		plus = "+" + strconv.Itoa(*w.upstream.Ahead)
	}
	minus := "0"
	if *w.upstream.Behind != 0 {
		minus = "-" + strconv.Itoa(*w.upstream.Behind)
	}
	return plus + "/" + minus
}

type Repo struct {
	inputPath string
	commonDir string
	name      string
	root      *string
	worktrees []*Worktree
}

// ---------------------------------------------------------------------------
// 路径与仓库解析
// ---------------------------------------------------------------------------

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }

// realpath 近似 Python 的 os.path.realpath：路径不存在时也尽量解析已存在的部分。
func realpath(p string) string {
	if p == "" {
		return p
	}
	clean := filepath.Clean(p)
	if r, err := filepath.EvalSymlinks(clean); err == nil {
		return r
	}
	parent := filepath.Dir(clean)
	if parent == clean { // 到根了
		return clean
	}
	return filepath.Join(realpath(parent), filepath.Base(clean))
}

func expandUser(p string) string {
	if p == "~" {
		if h, err := os.UserHomeDir(); err == nil {
			return h
		}
		return p
	}
	if strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, p[len("~/"):])
		}
	}
	return p
}

// resolveRepo 把用户给的路径解析成 (commonDir, name, root)。
func resolveRepo(inputPath string) (string, string, *string, error) {
	p := expandUser(inputPath)
	info, statErr := os.Stat(p)
	if statErr != nil {
		return "", "", nil, errors.New("路径不存在: " + inputPath)
	}
	if !info.IsDir() {
		return "", "", nil, errors.New("不是目录（是一个文件）: " + inputPath)
	}

	var commonDir string
	res := runGit("-C", p, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if res.code != 0 {
		// 老版本 git 没有 --path-format
		res2 := runGit("-C", p, "rev-parse", "--git-common-dir")
		if res2.code != 0 {
			msg := "不是 Git 仓库（或不可访问）: " + inputPath
			if s := strings.TrimSpace(res2.stderr); s != "" {
				msg += " [" + s + "]"
			}
			return "", "", nil, errors.New(msg)
		}
		rel := strings.TrimSpace(res2.stdout)
		if filepath.IsAbs(rel) {
			commonDir = rel
		} else {
			commonDir = filepath.Join(p, rel)
		}
	} else {
		commonDir = strings.TrimSpace(res.stdout)
	}
	commonDir = realpath(commonDir)

	// 仓库主工作树 / bare 判断
	var root *string
	top := runGit("-C", p, "rev-parse", "--show-toplevel")
	if top.code == 0 {
		root = strPtr(strings.TrimSpace(top.stdout))
	}
	bareRes := runGit("-C", p, "rev-parse", "--is-bare-repository")
	isBare := bareRes.code == 0 && strings.TrimSpace(bareRes.stdout) == "true"

	var name string
	if isBare {
		name = strings.TrimSuffix(filepath.Base(strings.TrimRight(commonDir, "/")), ".git")
		if root == nil {
			root = strPtr(commonDir)
		}
	} else if root != nil {
		name = filepath.Base(realpath(*root))
	} else {
		name = filepath.Base(strings.TrimRight(commonDir, "/"))
		if name == ".git" {
			name = filepath.Base(filepath.Dir(commonDir))
		}
	}
	if name == "" || name == "." || name == string(filepath.Separator) {
		name = "(未命名仓库)"
	}
	return commonDir, name, root, nil
}

// ---------------------------------------------------------------------------
// 单个 worktree 的巡检
// ---------------------------------------------------------------------------

func inspectWorktree(e worktreeEntry, repo *Repo, untrackedMode string) *Worktree {
	rawPath := e.path
	wt := &Worktree{
		repoName:  repo.name,
		repoRoot:  repo.root,
		commonDir: repo.commonDir,
		path:      rawPath,
		bare:      e.bare,
		locked:    e.locked,
		prunable:  e.prunable,
		problems:  []string{},
		errs:      []string{},
	}

	if st, err := os.Stat(rawPath); err == nil && st.IsDir() {
		wt.pathExists = true
	}
	if wt.bare {
		wt.isMain = realpath(rawPath) == repo.commonDir
	} else {
		wt.isMain = realpath(filepath.Join(rawPath, ".git")) == repo.commonDir
	}

	// ---- HEAD ----
	sha := e.head
	switch {
	case e.bare:
		wt.head = Head{State: "branch", Commit: shaPtr(sha)}
		wt.addProblem("bare")
	case e.detached:
		wt.head = Head{State: "detached", Commit: shaPtr(sha)}
		wt.addProblem("detached")
	case e.branch != "":
		ref := e.branch
		short := strings.TrimPrefix(ref, "refs/heads/")
		if sha == "" || sha == allZeroSHA {
			wt.head = Head{State: "unborn", Branch: strPtr(short), BranchRef: strPtr(ref)}
			wt.addProblem("unborn")
		} else {
			wt.head = Head{State: "branch", Branch: strPtr(short), BranchRef: strPtr(ref), Commit: strPtr(sha)}
		}
	default:
		wt.head = Head{State: "unknown", Commit: shaPtr(sha)}
		wt.addProblem("head_unknown")
	}

	// ---- 路径失效 / prunable / locked ----
	if e.prunable != nil {
		wt.addProblem("prunable")
	}
	if !wt.pathExists {
		wt.addProblem("path_missing")
	}
	if e.locked != nil {
		wt.addProblem("locked")
	}

	if !wt.pathExists {
		// 路径没了：不执行任何 per-worktree 命令，明细明确留空（不填 0）
		if wt.bare {
			wt.upstream = Upstream{State: "not_applicable"}
		} else {
			wt.upstream = Upstream{State: "unknown"}
		}
		return wt
	}
	if wt.bare {
		// bare 主仓库本身没有工作区，没有改动/未跟踪/upstream 的概念
		wt.upstream = Upstream{State: "not_applicable"}
		return wt
	}

	// ---- 该 worktree 是否仍然可用 ----
	inside := runGit("-C", rawPath, "rev-parse", "--is-inside-work-tree")
	if !(inside.code == 0 && strings.TrimSpace(inside.stdout) == "true") {
		wt.addProblem("not_a_worktree")
		if msg := strings.TrimSpace(inside.stderr); msg != "" {
			wt.errs = append(wt.errs, msg)
		}
		wt.upstream = Upstream{State: "unknown"}
		return wt
	}

	// ---- 状态统计 ----
	uflag := "--untracked-files=all"
	if untrackedMode == "normal" {
		uflag = "--untracked-files=normal"
	}
	st := runGit("-C", rawPath, "status", "--porcelain=v1", "-z", uflag, "--ignored=no")
	if st.code != 0 {
		wt.addProblem("status_error")
		if msg := strings.TrimSpace(st.stderr); msg != "" {
			wt.errs = append(wt.errs, msg)
		}
	} else {
		tracked, untracked, conflicts, _ := parseStatusPorcelainZ(st.stdout)
		wt.tracked, wt.untracked, wt.conflicts = intPtr(tracked), intPtr(untracked), intPtr(conflicts)
		if conflicts > 0 {
			wt.addProblem("conflicts")
		}
	}

	// ---- upstream（纯本地配置 + 本地引用，绝不 fetch）----
	if wt.head.State != "branch" {
		// detached / unborn / unknown：没有可追踪的分支
		wt.upstream = Upstream{State: "not_applicable"}
		return wt
	}

	branch := *wt.head.Branch
	remote := configGet(rawPath, "branch."+branch+".remote")
	merge := configGet(rawPath, "branch."+branch+".merge")
	if remote == nil || merge == nil {
		wt.upstream = Upstream{State: "none"}
		wt.addProblem("no_upstream")
		return wt
	}

	var upRef, upName string
	if *remote == "." {
		upRef = *merge
		upName = strings.TrimPrefix(*merge, "refs/heads/")
	} else {
		shortMerge := strings.TrimPrefix(*merge, "refs/heads/")
		upRef = "refs/remotes/" + *remote + "/" + shortMerge
		upName = *remote + "/" + shortMerge
	}

	up := Upstream{State: "unknown", Name: strPtr(upName), Ref: strPtr(upRef)}

	verify := runGit("-C", rawPath, "rev-parse", "--verify", "--quiet", "--end-of-options", upRef)
	if verify.code != 0 || strings.TrimSpace(verify.stdout) == "" {
		up.State = "missing_ref"
		wt.addProblem("upstream_ref_missing")
		wt.upstream = up
		return wt
	}
	up.RefCommit = strPtr(strings.TrimSpace(verify.stdout))

	if dateRes := runGit("-C", rawPath, "log", "-1", "--format=%cI", *up.RefCommit); dateRes.code == 0 {
		if d := strings.TrimSpace(dateRes.stdout); d != "" {
			up.RefDate = strPtr(d)
		}
	}

	if wt.head.Commit == nil {
		wt.upstream = up // state 仍是 unknown
		return wt
	}

	lr := runGit("-C", rawPath, "rev-list", "--left-right", "--count", upRef+"..."+*wt.head.Commit)
	if left, right, ok := parseLeftRightCount(lr.stdout); lr.code == 0 && ok {
		// A...B 的左计数 = 只在上游 = behind；右计数 = 只在本地 = ahead
		up.Behind, up.Ahead, up.State = intPtr(left), intPtr(right), "ok"
	}
	wt.upstream = up
	return wt
}

// shaPtr：空串或全零 SHA 视为未知 → nil（JSON null）。
func shaPtr(sha string) *string {
	if sha == "" || sha == allZeroSHA {
		return nil
	}
	return &sha
}

// inspectRepo 巡检一个输入路径。
func inspectRepo(inputPath, untrackedMode string) (*Repo, string) {
	commonDir, name, root, err := resolveRepo(inputPath)
	if err != nil {
		return nil, err.Error()
	}

	listRes := runGit("-C", expandUser(inputPath), "worktree", "list", "--porcelain")
	if listRes.code != 0 {
		return nil, "git worktree list 失败: " + strings.TrimSpace(listRes.stderr)
	}

	entries := parseWorktreePorcelain(listRes.stdout)
	repo := &Repo{inputPath: inputPath, commonDir: commonDir, name: name, root: root}

	// 主工作树排最前，其余按路径排序
	sort.SliceStable(entries, func(i, j int) bool {
		ki, kj := mainSortKey(entries[i], commonDir), mainSortKey(entries[j], commonDir)
		if ki != kj {
			return ki < kj
		}
		return entries[i].path < entries[j].path
	})

	for _, e := range entries {
		repo.worktrees = append(repo.worktrees, inspectWorktree(e, repo, untrackedMode))
	}
	return repo, ""
}

func mainSortKey(e worktreeEntry, commonDir string) int {
	var isMain bool
	if e.bare {
		isMain = realpath(e.path) == commonDir
	} else {
		isMain = realpath(filepath.Join(e.path, ".git")) == commonDir
	}
	if isMain {
		return 0
	}
	return 1
}

// ---------------------------------------------------------------------------
// JSON 契约（字段名与顺序与既有版本保持一致）
// ---------------------------------------------------------------------------

type jsonHead struct {
	State       string  `json:"state"`
	Branch      *string `json:"branch"`
	BranchRef   *string `json:"branch_ref"`
	Commit      *string `json:"commit"`
	ShortCommit *string `json:"short_commit"`
}

type jsonUpstream struct {
	State         string  `json:"state"`
	Name          *string `json:"name"`
	Ref           *string `json:"ref"`
	Ahead         *int    `json:"ahead"`
	Behind        *int    `json:"behind"`
	BasedOn       string  `json:"based_on"`
	RefCommit     *string `json:"ref_commit"`
	RefCommitDate *string `json:"ref_commit_date"`
}

type jsonWorktree struct {
	RepoName        string       `json:"repo_name"`
	RepoRoot        *string      `json:"repo_root"`
	CommonDir       string       `json:"common_dir"`
	Path            string       `json:"path"`
	IsMainWorktree  bool         `json:"is_main_worktree"`
	IsBare          bool         `json:"is_bare"`
	PathExists      bool         `json:"path_exists"`
	Locked          *string      `json:"locked"`
	Prunable        *string      `json:"prunable"`
	Head            jsonHead     `json:"head"`
	TrackedModified *int         `json:"tracked_modified"`
	Untracked       *int         `json:"untracked"`
	Conflicts       *int         `json:"conflicts"`
	Upstream        jsonUpstream `json:"upstream"`
	Problems        []string     `json:"problems"`
	ProblemLabels   []string     `json:"problem_labels"`
	Errors          []string     `json:"errors"`
}

type jsonRepo struct {
	InputPath     string         `json:"input_path"`
	RepoName      string         `json:"repo_name"`
	RepoRoot      *string        `json:"repo_root"`
	CommonDir     string         `json:"common_dir"`
	WorktreeCount int            `json:"worktree_count"`
	Worktrees     []jsonWorktree `json:"worktrees"`
}

type jsonSummary struct {
	Repos                 int `json:"repos"`
	Worktrees             int `json:"worktrees"`
	DirtyWorktrees        int `json:"dirty_worktrees"`
	WorktreesWithProblems int `json:"worktrees_with_problems"`
	FailedInputs          int `json:"failed_inputs"`
}

type jsonError struct {
	InputPath   string `json:"input_path"`
	Error       string `json:"error"`
	Kind        string `json:"kind"`
	DuplicateOf string `json:"duplicate_of,omitempty"`
}

type jsonDoc struct {
	Schema        int         `json:"schema"`
	Tool          string      `json:"tool"`
	Version       string      `json:"version"`
	GeneratedAt   string      `json:"generated_at"`
	ReadOnly      bool        `json:"read_only"`
	Fetched       bool        `json:"fetched"`
	UntrackedMode string      `json:"untracked_mode"`
	Notes         []string    `json:"notes"`
	Summary       jsonSummary `json:"summary"`
	Repos         []jsonRepo  `json:"repos"`
	Errors        []jsonError `json:"errors"`
}

func (w *Worktree) toJSON() jsonWorktree {
	problems := w.problems
	if problems == nil {
		problems = []string{}
	}
	labels := make([]string, 0, len(problems))
	for _, p := range problems {
		if l, ok := problemLabels[p]; ok {
			labels = append(labels, l)
		} else {
			labels = append(labels, p)
		}
	}
	errs := w.errs
	if errs == nil {
		errs = []string{}
	}
	return jsonWorktree{
		RepoName:       w.repoName,
		RepoRoot:       w.repoRoot,
		CommonDir:      w.commonDir,
		Path:           w.path,
		IsMainWorktree: w.isMain,
		IsBare:         w.bare,
		PathExists:     w.pathExists,
		Locked:         w.locked,
		Prunable:       w.prunable,
		Head: jsonHead{
			State:       w.head.State,
			Branch:      w.head.Branch,
			BranchRef:   w.head.BranchRef,
			Commit:      w.head.Commit,
			ShortCommit: w.head.shortCommit(),
		},
		TrackedModified: w.tracked,
		Untracked:       w.untracked,
		Conflicts:       w.conflicts,
		Upstream: jsonUpstream{
			State:         w.upstream.State,
			Name:          w.upstream.Name,
			Ref:           w.upstream.Ref,
			Ahead:         w.upstream.Ahead,
			Behind:        w.upstream.Behind,
			BasedOn:       "local-ref-no-fetch",
			RefCommit:     w.upstream.RefCommit,
			RefCommitDate: w.upstream.RefDate,
		},
		Problems:      problems,
		ProblemLabels: labels,
		Errors:        errs,
	}
}

func (r *Repo) toJSON() jsonRepo {
	wts := make([]jsonWorktree, 0, len(r.worktrees))
	for _, w := range r.worktrees {
		wts = append(wts, w.toJSON())
	}
	return jsonRepo{
		InputPath:     r.inputPath,
		RepoName:      r.name,
		RepoRoot:      r.root,
		CommonDir:     r.commonDir,
		WorktreeCount: len(r.worktrees),
		Worktrees:     wts,
	}
}

func summarize(repos []*Repo, errs []jsonError) jsonSummary {
	s := jsonSummary{Repos: len(repos)}
	for _, r := range repos {
		s.Worktrees += len(r.worktrees)
		for _, w := range r.worktrees {
			dirty := (w.tracked != nil && *w.tracked > 0) || (w.untracked != nil && *w.untracked > 0)
			if dirty {
				s.DirtyWorktrees++
			}
			if len(w.problems) > 0 {
				s.WorktreesWithProblems++
			}
		}
	}
	for _, e := range errs {
		if e.Kind == "error" {
			s.FailedInputs++
		}
	}
	return s
}

func buildDocument(repos []*Repo, errs []jsonError, untrackedMode string) jsonDoc {
	if errs == nil {
		errs = []jsonError{}
	}
	reposJSON := make([]jsonRepo, 0, len(repos))
	for _, r := range repos {
		reposJSON = append(reposJSON, r.toJSON())
	}
	return jsonDoc{
		Schema:        1,
		Tool:          prog,
		Version:       version,
		GeneratedAt:   time.Now().Format("2006-01-02T15:04:05-07:00"),
		ReadOnly:      true,
		Fetched:       false,
		UntrackedMode: untrackedMode,
		Notes: []string{
			"ahead/behind 完全基于本地已有引用计算，不执行 fetch；远端有新提交时不会反映。",
			"tracked_modified 含暂存、未暂存与冲突条目（重命名/拷贝各计 1）。",
			"untracked 为未跟踪文件数（all 模式展开目录逐个计数），不含 ignored。",
		},
		Summary: summarize(repos, errs),
		Repos:   reposJSON,
		Errors:  errs,
	}
}
