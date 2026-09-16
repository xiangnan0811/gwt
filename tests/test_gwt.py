#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""gwt 的测试套件（只用标准库 unittest）。

覆盖：正常分支 / 脏工作区 / 无 upstream / upstream 引用本地缺失 /
detached HEAD / 路径含空格 / 中文名 / 路径失效(prunable) / bare / unborn /
locked / 重命名计数 / 子目录与 worktree 作为输入 / 去重 / 退出码 /
只读性（.git/index 不被回写、工作区文件不变）/ 不 fetch / 命令白名单。

被测目标是 Go 实现编译出的二进制（默认 ./gwt），本套件只做黑盒验收：
调用 CLI、解析 JSON/表格、比对仓库快照。解析器与列宽算法的单元测试随实现
一起移到了 Go（git_test.go / table_test.go），用 `go test ./...` 运行。

用法：
    go build -o gwt .                              # 先编译
    python3 -m unittest discover -s tests -t . -v  # 再跑黑盒验收
    GWT_BIN=/path/to/gwt python3 tests/test_gwt.py  # 指定别的二进制
"""

from __future__ import annotations

import hashlib
import json
import os
import shutil
import stat
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
WORKSPACE = HERE.parent
GWT_BIN = os.environ.get("GWT_BIN") or str(WORKSPACE / "gwt")

sys.path.insert(0, str(HERE))
import fixtures  # noqa: E402

#: 在装 git 垫片之前抓取真实 git 路径
REAL_GIT = shutil.which("git")

FIX = {}


def setUpModule():
    global FIX
    if not os.path.exists(GWT_BIN):
        raise RuntimeError(
            f"找不到被测二进制 {GWT_BIN}；先在项目根目录执行 go build -o gwt .")
    FIX = fixtures.build_fixture(tempfile.mkdtemp(prefix="gwt-test-"))
    # 让 origin/main 再前进 1 个提交，但**不**在 alpha 里 fetch：
    # 用来证明 gwt 不会 fetch，且 ahead/behind 只基于本地引用。
    other = Path(FIX["root"]) / "_other_clone"
    (other / "remote3.txt").write_text("r3\n", encoding="utf-8")
    fixtures.git("add", "-A", cwd=other)
    fixtures.git("commit", "-m", "remote 3", cwd=other)
    fixtures.git("push", "-q", "origin", "main", cwd=other)
    # locked worktree（在 alpha-feature 上加锁）
    fixtures.git("worktree", "lock", FIX["alpha_feature"], cwd=FIX["alpha"])
    FIX["_other_clone"] = str(other)


def tearDownModule():
    root = FIX.get("root")
    if root and os.path.isdir(root):
        shutil.rmtree(root, ignore_errors=True)


def run_tool(*args, env=None, cwd=None):
    e = dict(os.environ)
    if env:
        e.update(env)
    proc = subprocess.run(
        [GWT_BIN, *[str(a) for a in args]],
        cwd=str(cwd) if cwd else None,
        env=e,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        encoding="utf-8",
        errors="replace",
    )
    return proc.returncode, proc.stdout, proc.stderr


def run_json(*args, **kw):
    rc, out, err = run_tool("--json", *args, **kw)
    try:
        doc = json.loads(out)
    except json.JSONDecodeError as exc:  # pragma: no cover
        raise AssertionError(f"JSON 解析失败: {exc}\nstdout={out[:2000]}\nstderr={err}")
    return rc, doc, err


def wt_of(doc, path):
    for repo in doc["repos"]:
        for w in repo["worktrees"]:
            if w["path"] == path:
                return w
    raise AssertionError(f"未找到 worktree: {path}")


def snapshot_tree(root):
    """整棵目录的文件清单：相对路径 -> (size, mtime_ns, sha256)。"""
    snap = {}
    for dirpath, _dirnames, filenames in os.walk(root):
        for fn in filenames:
            p = os.path.join(dirpath, fn)
            rel = os.path.relpath(p, root)
            try:
                st = os.lstat(p)
                if stat.S_ISLNK(st.st_mode):
                    snap[rel] = ("link", os.readlink(p))
                    continue
                with open(p, "rb") as f:
                    digest = hashlib.sha256(f.read()).hexdigest()
                snap[rel] = (st.st_size, st.st_mtime_ns, digest)
            except OSError as exc:  # pragma: no cover
                snap[rel] = ("error", str(exc))
    return snap


# 说明：原先这里的 TestParsers（解析 porcelain / status -z / 左右计数 / 宽度 /
# 截断）测的是 Python 内部函数。实现换成 Go 之后，等价且更细的单元测试放在
# Go 侧：git_test.go（解析器、shaPtr）与 table_test.go（宽度、截断、列宽收缩）。
# 本文件保留的是黑盒验收：只通过 CLI 观察行为。


class TestWorktreeInventory(unittest.TestCase):
    def test_alpha_main_normal_with_upstream(self):
        rc, doc, _ = run_json(FIX["alpha"])
        self.assertEqual(rc, 0)
        w = wt_of(doc, FIX["alpha"])
        self.assertEqual(w["repo_name"], "alpha")
        self.assertEqual(w["head"]["state"], "branch")
        self.assertEqual(w["head"]["branch"], "main")
        self.assertTrue(w["is_main_worktree"])
        self.assertEqual(w["upstream"]["state"], "ok")
        self.assertEqual(w["upstream"]["name"], "origin/main")
        self.assertEqual(w["upstream"]["ahead"], 1)
        self.assertEqual(w["upstream"]["behind"], 2)
        self.assertEqual(w["problems"], [])

    def test_ahead_behind_only_uses_local_refs(self):
        """origin/main 已经在远端前进了，但 gwt 不该 fetch。

        证据链：
          1) 跑之前/之后，本地 refs/remotes/origin/main 的 sha 完全不变；
          2) .git/FETCH_HEAD 的内容与 mtime 不变（真 fetch 必然重写它）；
          3) gwt 报的 ahead/behind 等于 git 按“本地引用”算出的值；
          4) 远端 main 确实已经超出本地引用（所以“不 fetch”是有意义的）。
        """
        repo = FIX["alpha"]
        local_ref = fixtures.git(
            "rev-parse", "refs/remotes/origin/main", cwd=repo).stdout.strip()
        fetch_head = Path(repo) / ".git" / "FETCH_HEAD"
        fh_before = None
        if fetch_head.exists():
            st = fetch_head.stat()
            fh_before = (hashlib.sha256(fetch_head.read_bytes()).hexdigest(),
                         st.st_mtime_ns)

        rc, doc, _ = run_json(repo)
        w = wt_of(doc, repo)

        after_ref = fixtures.git(
            "rev-parse", "refs/remotes/origin/main", cwd=repo).stdout.strip()
        self.assertEqual(local_ref, after_ref, "gwt 不应更新远端跟踪引用")

        if fh_before is not None:
            st = fetch_head.stat()
            self.assertEqual(
                fh_before,
                (hashlib.sha256(fetch_head.read_bytes()).hexdigest(), st.st_mtime_ns),
                "gwt 不应触碰 FETCH_HEAD（说明确实没有 fetch）")

        # 与 git 按本地引用计算的语义一致
        counts = fixtures.git(
            "rev-list", "--left-right", "--count",
            "refs/remotes/origin/main...HEAD", cwd=repo).stdout.split()
        behind_git, ahead_git = int(counts[0]), int(counts[1])
        self.assertEqual((w["upstream"]["ahead"], w["upstream"]["behind"]),
                         (ahead_git, behind_git))
        self.assertEqual(w["upstream"]["behind"], 2)
        self.assertEqual(w["upstream"]["ahead"], 1)
        self.assertEqual(w["upstream"]["based_on"], "local-ref-no-fetch")

        # 远端确实领先于本地引用：origin 里的 main 与本地记录不同，且本地记录是其祖先
        origin_main = fixtures.git("rev-parse", "main", cwd=FIX["origin"]).stdout.strip()
        self.assertNotEqual(origin_main, local_ref)
        n = fixtures.git("rev-list", "--count", f"{local_ref}..main",
                         cwd=FIX["origin"]).stdout.strip()
        self.assertGreaterEqual(int(n), 1,
                                "远端自本地 fetch 之后应当又有新提交")

        # 期望的“真 behind”（如果 fetch 就会变成它）= 本地引用视角 behind + 远端新增
        true_behind_if_fetched = behind_git + int(n)
        self.assertGreater(true_behind_if_fetched, w["upstream"]["behind"],
                           "若 gwt 偷偷 fetch，behind 会变大；这里必须保持小值")

    def test_dirty_counts_and_untracked_modes(self):
        rc, doc, _ = run_json(FIX["alpha"])
        w = wt_of(doc, FIX["alpha"])
        # a.txt 改 + b.txt 删 + staged_new.txt 暂存新增 = 3
        self.assertEqual(w["tracked_modified"], 3)
        # u1, u2, udir/n1, udir/n2 = 4（all 模式展开目录）
        self.assertEqual(w["untracked"], 4)

        rc2, doc2, _ = run_json("--untracked", "normal", FIX["alpha"])
        w2 = wt_of(doc2, FIX["alpha"])
        # normal 模式：udir 目录只算 1 条 → 3
        self.assertEqual(w2["untracked"], 3)

    def test_detached_head(self):
        rc, doc, _ = run_json(FIX["alpha"])
        w = wt_of(doc, FIX["alpha_detached"])
        self.assertEqual(w["head"]["state"], "detached")
        self.assertIsNone(w["head"]["branch"])
        self.assertIn("detached", w["problems"])
        self.assertEqual(w["upstream"]["state"], "not_applicable")
        self.assertIsNone(w["upstream"]["ahead"])
        # 实际 detached 的 sha 应与 git 一致
        real = fixtures.git("rev-parse", "HEAD", cwd=FIX["alpha_detached"]).stdout.strip()
        self.assertEqual(w["head"]["commit"], real)
        self.assertEqual(w["tracked_modified"], 0)
        self.assertEqual(w["untracked"], 0)

    def test_no_upstream(self):
        rc, doc, _ = run_json(FIX["beta"])
        w = wt_of(doc, FIX["beta"])
        self.assertEqual(w["upstream"]["state"], "none")
        self.assertIsNone(w["upstream"]["ahead"])
        self.assertIsNone(w["upstream"]["behind"])
        self.assertIn("no_upstream", w["problems"])

    def test_upstream_configured_but_ref_missing_locally(self):
        rc, doc, _ = run_json(FIX["alpha"])
        w = wt_of(doc, FIX["alpha_feature"])
        self.assertEqual(w["upstream"]["state"], "missing_ref")
        self.assertEqual(w["upstream"]["name"], "origin/feature/x")
        self.assertEqual(w["upstream"]["ref"], "refs/remotes/origin/feature/x")
        self.assertIsNone(w["upstream"]["ahead"])
        self.assertIsNone(w["upstream"]["behind"])
        self.assertIn("upstream_ref_missing", w["problems"])

    def test_missing_path_is_not_zero(self):
        rc, doc, _ = run_json(FIX["alpha"])
        w = wt_of(doc, FIX["alpha_todelete"])
        self.assertFalse(w["path_exists"])
        self.assertIn("path_missing", w["problems"])
        self.assertIn("prunable", w["problems"])
        # 关键：不能把失效路径填成 0
        self.assertIsNone(w["tracked_modified"])
        self.assertIsNone(w["untracked"])
        self.assertIsNone(w["upstream"]["ahead"])

    def test_locked_worktree(self):
        rc, doc, _ = run_json(FIX["alpha"])
        w = wt_of(doc, FIX["alpha_feature"])
        self.assertIn("locked", w["problems"])
        self.assertIsNotNone(w["locked"])

    def test_path_with_spaces_and_staged_rename(self):
        rc, doc, _ = run_json(FIX["beta"])
        w = wt_of(doc, FIX["beta_wt"])
        self.assertEqual(w["path"], FIX["beta_wt"])
        self.assertIn(" ", w["path"])
        self.assertEqual(w["head"]["branch"], "feat/space")
        # git mv 已暂存：只算 1 条（-z 下原路径那条不能被重复计数）
        self.assertEqual(w["tracked_modified"], 1)
        self.assertEqual(w["untracked"], 0)

    def test_merge_conflict_is_reported(self):
        """真实 merge 冲突：conflicts>0、标记里有冲突，且工具不会去动它。"""
        repo = FIX["epsilon"]
        status_before = fixtures.git("status", "--porcelain", cwd=repo).stdout
        rc, doc, _ = run_json(repo)
        self.assertEqual(rc, 0)
        w = wt_of(doc, repo)
        self.assertGreaterEqual(w["conflicts"], 1)
        self.assertGreaterEqual(w["tracked_modified"], 1)
        self.assertIn("conflicts", w["problems"])
        # 冲突状态原样保留，没有被工具“顺手解决”或 reset
        after = fixtures.git("status", "--porcelain", cwd=repo).stdout
        self.assertEqual(status_before, after)
        self.assertTrue(os.path.exists(os.path.join(repo, ".git", "MERGE_HEAD")))

    def test_unborn_repo(self):
        rc, doc, _ = run_json(FIX["delta"])
        w = wt_of(doc, FIX["delta"])
        self.assertEqual(w["head"]["state"], "unborn")
        self.assertIsNone(w["head"]["commit"])
        self.assertEqual(w["head"]["branch"], "main")
        self.assertEqual(w["tracked_modified"], 0)
        self.assertEqual(w["untracked"], 2)
        self.assertIn("unborn", w["problems"])

    def test_bare_repo_and_its_linked_worktree(self):
        rc, doc, _ = run_json(FIX["gamma"])
        self.assertEqual(rc, 0)
        paths = [w["path"] for repo in doc["repos"] for w in repo["worktrees"]]
        self.assertIn(FIX["gamma"], paths)
        self.assertIn(FIX["gamma_wt"], paths)
        bare = wt_of(doc, FIX["gamma"])
        self.assertTrue(bare["is_bare"])
        self.assertIn("bare", bare["problems"])
        self.assertIsNone(bare["tracked_modified"])
        wtwt = wt_of(doc, FIX["gamma_wt"])
        self.assertEqual(wtwt["head"]["branch"], "wt")
        self.assertFalse(wtwt["is_bare"])

    def test_chinese_repo_and_branch_names(self):
        rc, doc, _ = run_json(FIX["zh"])
        self.assertEqual(rc, 0)
        repo = doc["repos"][0]
        self.assertEqual(repo["repo_name"], "中文仓库")
        w = wt_of(doc, FIX["zh_wt"])
        self.assertEqual(w["head"]["branch"], "特性/一")
        self.assertEqual(w["repo_name"], "中文仓库")

    def test_upstream_pointing_at_local_branch(self):
        """branch.<x>.remote = "." 时 upstream 是本地分支，不涉及 remote。"""
        rc, doc, _ = run_json(FIX["zh"])
        w = wt_of(doc, FIX["zh"])
        self.assertEqual(w["upstream"]["state"], "ok")
        self.assertEqual(w["upstream"]["name"], "base")
        self.assertEqual(w["upstream"]["ref"], "refs/heads/base")
        self.assertEqual(w["upstream"]["ahead"], 1)
        self.assertEqual(w["upstream"]["behind"], 0)

    def test_subdirectory_as_input(self):
        sub = Path(FIX["alpha"]) / "dir1"
        rc, doc, _ = run_json(sub)
        self.assertEqual(rc, 0)
        self.assertEqual(len(doc["repos"]), 1)
        self.assertEqual(doc["repos"][0]["repo_name"], "alpha")
        # 与直接用主工作树路径一致
        rc2, doc2, _ = run_json(FIX["alpha"])
        self.assertEqual(doc["repos"][0]["common_dir"], doc2["repos"][0]["common_dir"])

    def test_worktree_path_as_input_lists_all_worktrees(self):
        rc, doc, _ = run_json(FIX["alpha_feature"])
        self.assertEqual(rc, 0)
        n = len(doc["repos"][0]["worktrees"])
        self.assertEqual(n, 4)  # main + feature + detached + todelete

    def test_relative_path_input(self):
        rc, doc, _ = run_json("alpha", cwd=FIX["root"])
        self.assertEqual(rc, 0)
        self.assertEqual(doc["repos"][0]["repo_name"], "alpha")


class TestCliBehaviour(unittest.TestCase):
    def test_invalid_inputs_exit_code(self):
        rc, _out, err = run_tool("/nonexistent-xyz")
        self.assertEqual(rc, 1)
        self.assertIn("路径不存在", err)

    def test_not_a_repo_directory(self):
        d = tempfile.mkdtemp(prefix="gwt-notrepo-")
        try:
            rc, _out, err = run_tool(d)
            self.assertEqual(rc, 1)
            self.assertIn("不是 Git 仓库", err)
        finally:
            shutil.rmtree(d, ignore_errors=True)

    def test_file_as_input(self):
        p = Path(FIX["alpha"]) / "a.txt"
        rc, _out, err = run_tool(p)
        self.assertEqual(rc, 1)
        self.assertIn("不是目录", err)

    def test_partial_failure_still_shows_good_repo(self):
        rc, out, err = run_tool(FIX["alpha"], "/nonexistent-xyz")
        self.assertEqual(rc, 1)
        self.assertIn("alpha", out)
        self.assertIn("路径不存在", err)

    def test_duplicate_input_deduped(self):
        rc, doc, err = run_json(FIX["alpha"], FIX["alpha_feature"])
        self.assertEqual(rc, 0)
        self.assertEqual(len(doc["repos"]), 1)
        kinds = [e["kind"] for e in doc["errors"]]
        self.assertIn("duplicate", kinds)

    def test_strict_exit_code(self):
        rc, _doc, _ = run_json(FIX["beta"])
        self.assertEqual(rc, 0)  # 默认：有问题但输入没失败 → 0
        rc2, _out, _err = run_tool("--strict", FIX["beta"])
        self.assertEqual(rc2, 1)  # --strict：有需关注项 → 1

    def test_table_output_contains_key_facts(self):
        rc, out, _err = run_tool("--no-truncate", FIX["alpha"], FIX["beta"], FIX["delta"])
        self.assertEqual(rc, 0)
        self.assertIn("worktree 路径", out)
        self.assertIn("origin/main", out)
        self.assertIn("(detached)", out)
        self.assertIn("(无)", out)
        self.assertIn("(unborn)", out)
        self.assertIn("+1/-2", out)
        self.assertIn("路径失效", out)
        # 失效路径的改动/未跟踪必须是 "-"，不能是 0
        for line in out.splitlines():
            if "alpha-todelete" in line:
                cells = line.split()
                self.assertIn("-", cells)
                break
        else:
            self.fail("表格里没找到 alpha-todelete 行")

    def test_no_truncate_shows_full_path(self):
        rc, out, _ = run_tool("--no-truncate", FIX["alpha_detached"])
        self.assertEqual(rc, 0)
        self.assertIn(FIX["alpha_detached"], out)

    def test_narrow_and_wide_widths_do_not_crash(self):
        for width in ("40", "80", "200", "1000"):
            rc, out, _err = run_tool("--width", width, FIX["alpha"])
            self.assertEqual(rc, 0, f"width={width} 失败")
            self.assertIn("alpha", out)

    def test_json_keys_contract(self):
        rc, doc, _ = run_json(FIX["alpha"])
        for key in ("schema", "tool", "version", "generated_at", "read_only",
                    "fetched", "untracked_mode", "summary", "repos", "errors"):
            self.assertIn(key, doc)
        w = doc["repos"][0]["worktrees"][0]
        for key in ("repo_name", "repo_root", "common_dir", "path", "is_main_worktree",
                    "is_bare", "path_exists", "locked", "prunable", "head",
                    "tracked_modified", "untracked", "conflicts", "upstream",
                    "problems", "problem_labels", "errors"):
            self.assertIn(key, w)
        for key in ("state", "branch", "branch_ref", "commit", "short_commit"):
            self.assertIn(key, w["head"])
        for key in ("state", "name", "ref", "ahead", "behind", "based_on",
                    "ref_commit", "ref_commit_date"):
            self.assertIn(key, w["upstream"])

    def test_json_is_machine_parseable_with_unicode(self):
        rc, doc, _ = run_json(FIX["zh"])
        raw = json.dumps(doc, ensure_ascii=False)
        self.assertIn("中文仓库", raw)
        self.assertEqual(rc, 0)


class TestReadOnlyGuarantee(unittest.TestCase):
    """最重要的部分：证明工具不修改目标仓库。"""

    def test_working_tree_and_git_dir_untouched(self):
        repo = FIX["alpha"]
        # 把 index 的 stat 缓存弄脏（改 mtime），这样普通 git status 会想回写 index
        os.utime(os.path.join(repo, "c.txt"), None)
        os.utime(os.path.join(repo, "dir1", "f1.txt"), None)
        before = snapshot_tree(repo)

        rc, _out, _err = run_tool(repo)                    # 表格模式
        self.assertEqual(rc, 0)
        rc2, _out2, _err2 = run_tool("--json", repo)        # JSON 模式
        self.assertEqual(rc2, 0)

        after = snapshot_tree(repo)
        self.assertEqual(before, after, "gwt 运行后目标仓库发生了变化")
        # 明确单独检查 index（git status 最容易顺手回写的东西）
        self.assertIn(os.path.join(".git", "index"), before)
        self.assertEqual(before[os.path.join(".git", "index")],
                         after[os.path.join(".git", "index")])

    def test_no_lock_or_temp_files_created(self):
        repo = FIX["beta"]
        before = set(snapshot_tree(repo).keys())
        run_tool(repo)
        after = set(snapshot_tree(repo).keys())
        self.assertEqual(before, after)
        self.assertFalse(os.path.exists(os.path.join(repo, ".git", "index.lock")))

    def test_only_whitelisted_git_commands_are_used(self):
        """用 git 垫片记录所有 git 调用，验证没有一个是会改状态的。

        垫片按 \\x1f 分隔记录每个 argv，因此含空格的路径也能精确解析。
        """
        if not REAL_GIT:  # pragma: no cover
            self.skipTest("找不到真实 git")
        tmp = tempfile.mkdtemp(prefix="gwt-shim-")
        try:
            shim_dir = Path(tmp) / "bin"
            shim_dir.mkdir()
            log = Path(tmp) / "git-calls.log"
            shim = shim_dir / "git"
            shim.write_text(
                "#!/bin/sh\n"
                'for a in "$@"; do printf "%s\\037" "$a" >> "' + str(log) + '"; done\n'
                'printf "\\n" >> "' + str(log) + '"\n'
                f'exec "{REAL_GIT}" "$@"\n',
                encoding="utf-8",
            )
            shim.chmod(0o755)
            env = {"PATH": f"{shim_dir}:{os.environ.get('PATH', '')}"}
            rc, _out, err = run_tool(FIX["alpha"], FIX["beta_wt"], FIX["gamma"],
                                     FIX["delta"], FIX["epsilon"], env=env)
            self.assertEqual(rc, 0, err)
            raw = log.read_text(encoding="utf-8")
            calls = [ln.split("\x1f")[:-1] for ln in raw.splitlines() if ln.strip("\x1f")]
        finally:
            shutil.rmtree(tmp, ignore_errors=True)

        self.assertTrue(calls, "垫片没有记录到任何 git 调用")

        allowed_global = {"--no-optional-locks", "-C"}
        allowed_sub = {"rev-parse", "status", "rev-list", "log", "worktree", "config"}
        forbidden_sub = {
            "fetch", "pull", "push", "checkout", "reset", "clean", "prune", "gc",
            "add", "commit", "rm", "mv", "update-ref", "switch", "restore", "merge",
            "rebase", "stash", "apply", "am", "tag", "repack", "update-index",
            "write-tree", "commit-tree", "symbolic-ref", "remote", "branch",
            "init", "clone", "submodule", "filter-branch", "notes", "rerere",
            "sparse-checkout",
        }
        for argv in calls:
            self.assertTrue(argv, "空调用")
            self.assertIn("--no-optional-locks", argv,
                          f"调用缺少 --no-optional-locks: {argv}")
            i = 0
            while i < len(argv) and argv[i] in allowed_global:
                i += 2 if argv[i] == "-C" else 1
            self.assertLess(i, len(argv), f"只有全局选项: {argv}")
            sub = argv[i]
            self.assertIn(sub, allowed_sub, f"白名单之外的 git 子命令: {argv}")
            self.assertNotIn(sub, forbidden_sub, f"危险子命令: {argv}")
            rest = argv[i + 1:]
            if sub == "worktree":
                self.assertEqual(rest[:1], ["list"], f"只允许 worktree list: {argv}")
            elif sub == "config":
                self.assertEqual(rest[:1], ["--get"], f"只允许 config --get: {argv}")
            elif sub == "log":
                self.assertEqual(rest[:1], ["-1"], f"只允许 log -1: {argv}")
            elif sub == "rev-list":
                self.assertIn("--count", rest, f"只允许 rev-list --count: {argv}")

    def test_worktree_registration_not_modified(self):
        """跑完之后 worktree 注册表不变（没有 prune、没有增删）。"""
        repo = FIX["alpha"]
        before = fixtures.git("worktree", "list", "--porcelain", cwd=repo).stdout
        run_tool(repo)
        after = fixtures.git("worktree", "list", "--porcelain", cwd=repo).stdout
        self.assertEqual(before, after)


if __name__ == "__main__":
    unittest.main(verbosity=2)
