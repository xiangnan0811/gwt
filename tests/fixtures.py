#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""构造 gwt 的测试/演示 fixture（全部在临时目录里，绝不碰真实项目）。

build_fixture(root) -> dict  返回各路径，供测试断言与演示使用。

覆盖场景：
  alpha        正常分支 + upstream + 真实 ahead/behind（先 fetch 过）
               dirty 主工作区（改动/暂存/删除/未跟踪文件/未跟踪目录）
               worktree: feature/x（upstream 配置存在但本地无该引用）
               worktree: "alpha detached"（路径含空格 + detached HEAD）
               worktree: alpha-todelete（目录被删 → prunable / 路径失效）
  beta         无 upstream；worktree "beta 空格 目录" 里有一次已暂存的重命名
  gamma.git    bare 仓库 + 指向它的 linked worktree
  delta        全新 git init，无提交（unborn）
  中文仓库      中文仓库名 + 中文 worktree 目录
"""

from __future__ import annotations

import os
import subprocess
import sys
from pathlib import Path

IDENT = ["-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid"]


def git(*args, cwd=None, check=True):
    proc = subprocess.run(
        ["git", *IDENT, *args],
        cwd=str(cwd) if cwd else None,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
        encoding="utf-8",
        errors="replace",
        env={**os.environ, "GIT_TERMINAL_PROMPT": "0", "GIT_CONFIG_NOSYSTEM": "1"},
    )
    if check and proc.returncode != 0:
        raise RuntimeError(f"fixture git {' '.join(args)} 失败:\n{proc.stdout}")
    return proc


def write(path: Path, content: str):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding="utf-8")


def build_fixture(root) -> dict:
    root = Path(root).resolve()
    root.mkdir(parents=True, exist_ok=True)
    out = {"root": str(root)}

    # ---------------------------------------------------------------- alpha
    origin = root / "origin.git"
    git("init", "--bare", "-b", "main", str(origin))

    alpha = root / "alpha"
    git("init", "-b", "main", str(alpha))
    write(alpha / "a.txt", "a\n")
    write(alpha / "b.txt", "b\n")
    write(alpha / "c.txt", "c\n")
    write(alpha / "dir1" / "f1.txt", "f1\n")
    git("add", "-A", cwd=alpha)
    git("commit", "-m", "init", cwd=alpha)
    git("remote", "add", "origin", str(origin), cwd=alpha)
    git("push", "-u", "origin", "main", cwd=alpha)

    # 让 origin/main 前进 2 个提交，然后在 alpha 里 fetch → 制造 behind=2
    other = root / "_other_clone"
    git("clone", "-q", str(origin), str(other))
    write(other / "remote1.txt", "r1\n")
    git("add", "-A", cwd=other)
    git("commit", "-m", "remote 1", cwd=other)
    write(other / "remote2.txt", "r2\n")
    git("add", "-A", cwd=other)
    git("commit", "-m", "remote 2", cwd=other)
    git("push", "-q", "origin", "main", cwd=other)
    git("fetch", "-q", "origin", cwd=alpha)
    # 本地再来 1 个提交 → ahead=1 behind=2（皆由本地引用算出）
    write(alpha / "local.txt", "l\n")
    git("add", "-A", cwd=alpha)
    git("commit", "-m", "local 1", cwd=alpha)

    # dirty：1 处修改 + 1 处删除 + 1 处新增暂存 = 3 条已跟踪改动
    write(alpha / "a.txt", "a changed\n")
    (alpha / "b.txt").unlink()
    write(alpha / "staged_new.txt", "s\n")
    git("add", "staged_new.txt", cwd=alpha)
    # 未跟踪：u1、u2 + udir/ 下 2 个文件 → all 模式 4，normal 模式 3
    write(alpha / "u1.txt", "u1\n")
    write(alpha / "u2.txt", "u2\n")
    write(alpha / "udir" / "n1.txt", "n1\n")
    write(alpha / "udir" / "n2.txt", "n2\n")

    # worktree: upstream 配置指向不存在的远端引用
    feat = root / "alpha-feature"
    git("worktree", "add", "-q", "-b", "feature/x", str(feat), "main", cwd=alpha)
    git("config", "branch.feature/x.remote", "origin", cwd=feat)
    git("config", "branch.feature/x.merge", "refs/heads/feature/x", cwd=feat)

    # worktree: 路径含空格 + detached HEAD
    det = root / "alpha detached"
    git("worktree", "add", "-q", "--detach", str(det), "main", cwd=alpha)

    # worktree: 目录稍后删掉 → prunable
    todelete = root / "alpha-todelete"
    git("worktree", "add", "-q", "-b", "tmp/doomed", str(todelete), "main", cwd=alpha)

    out["origin"] = str(origin)
    out["alpha"] = str(alpha)
    out["alpha_feature"] = str(feat)
    out["alpha_detached"] = str(det)
    out["alpha_todelete"] = str(todelete)

    # ---------------------------------------------------------------- beta
    beta = root / "beta"
    git("init", "-b", "work", str(beta))
    write(beta / "old_name.txt", "content\n")
    write(beta / "keep.txt", "keep\n")
    git("add", "-A", cwd=beta)
    git("commit", "-m", "beta init", cwd=beta)

    beta_wt = root / "beta 空格 目录"
    git("worktree", "add", "-q", "-b", "feat/space", str(beta_wt), "work", cwd=beta)
    # 已暂存的重命名：应只计 1 条已跟踪改动（验证 -z 解析不把原路径当第二条）
    git("mv", "old_name.txt", "new_name.txt", cwd=beta_wt)
    out["beta"] = str(beta)
    out["beta_wt"] = str(beta_wt)

    # --------------------------------------------------------------- gamma
    gamma = root / "gamma.git"
    git("init", "--bare", "-b", "main", str(gamma))
    gamma_wt = root / "gamma-wt"
    git("worktree", "add", "-q", "--orphan", "-b", "wt", str(gamma_wt), cwd=gamma)
    write(gamma_wt / "g.txt", "g\n")
    git("add", "-A", cwd=gamma_wt)
    git("commit", "-m", "gamma wt", cwd=gamma_wt)
    out["gamma"] = str(gamma)
    out["gamma_wt"] = str(gamma_wt)

    # --------------------------------------------------------------- delta
    delta = root / "delta"
    git("init", "-b", "main", str(delta))
    write(delta / "d1.txt", "d1\n")
    write(delta / "d2.txt", "d2\n")
    out["delta"] = str(delta)

    # ------------------------------------------------------------- epsilon
    # 真实冲突：merge 到一半停下，用来验证 conflicts / “冲突”标记
    eps = root / "epsilon"
    git("init", "-b", "main", str(eps))
    write(eps / "c.txt", "base\n")
    git("add", "-A", cwd=eps)
    git("commit", "-m", "base", cwd=eps)
    git("branch", "side", cwd=eps)
    write(eps / "c.txt", "main version\n")
    git("add", "-A", cwd=eps)
    git("commit", "-m", "main side of conflict", cwd=eps)
    git("checkout", "-q", "side", cwd=eps)
    write(eps / "c.txt", "side version\n")
    git("add", "-A", cwd=eps)
    git("commit", "-m", "side of conflict", cwd=eps)
    # 预期失败：留下 UU 冲突
    git("merge", "main", cwd=eps, check=False)
    out["epsilon"] = str(eps)

    # ------------------------------------------------------------ 中文仓库
    zh = root / "中文仓库"
    git("init", "-b", "main", str(zh))
    write(zh / "说明.md", "# 说明\n")
    git("add", "-A", cwd=zh)
    git("commit", "-m", "中文提交", cwd=zh)
    # upstream 指向本地分支（branch.main.remote = "."），不涉及任何 remote
    git("branch", "base", cwd=zh)
    git("config", "branch.main.remote", ".", cwd=zh)
    git("config", "branch.main.merge", "refs/heads/base", cwd=zh)
    write(zh / "第二个文件.md", "二\n")
    git("add", "-A", cwd=zh)
    git("commit", "-m", "第二个提交", cwd=zh)   # main 领先 base 1 个提交
    zh_wt = root / "中文仓库-工作树"
    git("worktree", "add", "-q", "-b", "特性/一", str(zh_wt), "main", cwd=zh)
    out["zh"] = str(zh)
    out["zh_wt"] = str(zh_wt)

    # 真正制造“路径失效”：直接把 worktree 目录删掉（不 prune，保持注册记录）
    import shutil
    shutil.rmtree(todelete)
    return out


def main():
    import tempfile

    if len(sys.argv) > 1:
        root = Path(sys.argv[1])
        info = build_fixture(root)
    else:
        tmp = tempfile.mkdtemp(prefix="gwt-demo-")
        info = build_fixture(tmp)
    for k, v in info.items():
        print(f"{k}={v}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
