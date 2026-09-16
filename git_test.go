package main

import "testing"

func TestParseWorktreePorcelain(t *testing.T) {
	text := "worktree /tmp/a\nHEAD 1111111111111111111111111111111111111111\n" +
		"branch refs/heads/main\n\n" +
		"worktree /tmp/b c\nHEAD 2222222222222222222222222222222222222222\n" +
		"detached\nlocked reason here\n\n" +
		"worktree /tmp/d\nHEAD 3333333333333333333333333333333333333333\n" +
		"branch refs/heads/x\nprunable gitdir file points to non-existent location\n"

	entries := parseWorktreePorcelain(text)
	if len(entries) != 3 {
		t.Fatalf("期望 3 个条目，得到 %d", len(entries))
	}
	if entries[0].path != "/tmp/a" || entries[0].branch != "refs/heads/main" {
		t.Errorf("第 0 条解析错误: %+v", entries[0])
	}
	if entries[1].path != "/tmp/b c" || !entries[1].detached {
		t.Errorf("第 1 条解析错误（含空格路径/detached）: %+v", entries[1])
	}
	if entries[1].locked == nil || *entries[1].locked != "reason here" {
		t.Errorf("locked 解析错误: %v", entries[1].locked)
	}
	if entries[2].prunable == nil ||
		*entries[2].prunable != "gitdir file points to non-existent location" {
		t.Errorf("prunable 解析错误: %v", entries[2].prunable)
	}
	if entries[1].bare || entries[2].bare {
		t.Error("bare 误判")
	}
}

func TestParseWorktreePorcelainBare(t *testing.T) {
	// bare 仓库：只有 worktree + bare 两行
	entries := parseWorktreePorcelain("worktree /srv/r.git\nbare\n")
	if len(entries) != 1 {
		t.Fatalf("期望 1 个条目，得到 %d", len(entries))
	}
	if !entries[0].bare {
		t.Error("bare 未识别")
	}
	if entries[0].branch != "" || entries[0].detached {
		t.Errorf("bare 条目不应有 branch/detached: %+v", entries[0])
	}
	if entries[0].locked != nil {
		t.Error("未锁定的条目 locked 应为 nil")
	}
}

func TestParseWorktreePorcelainLockedWithoutReason(t *testing.T) {
	entries := parseWorktreePorcelain("worktree /tmp/a\nHEAD 1111111111111111111111111111111111111111\nbranch refs/heads/m\nlocked\n")
	if len(entries) != 1 || entries[0].locked == nil {
		t.Fatalf("locked 未识别: %+v", entries)
	}
	if *entries[0].locked != "" {
		t.Errorf("无原因时应是空串，得到 %q", *entries[0].locked)
	}
}

func TestParseStatusPorcelainZCountsRenameOnce(t *testing.T) {
	// R  new \0 old \0  +  M  a.txt \0  +  ?? u.txt \0
	raw := "R  new.txt\x00old.txt\x00M  a.txt\x00?? u.txt\x00"
	tracked, untracked, conflicts, ignored := parseStatusPorcelainZ(raw)
	if tracked != 2 {
		t.Errorf("tracked 期望 2（重命名只算 1），得到 %d", tracked)
	}
	if untracked != 1 {
		t.Errorf("untracked 期望 1，得到 %d", untracked)
	}
	if conflicts != 0 || ignored != 0 {
		t.Errorf("conflicts/ignored 期望 0/0，得到 %d/%d", conflicts, ignored)
	}
}

func TestParseStatusPorcelainZCopyAndRename(t *testing.T) {
	raw := "C  copy.txt\x00src.txt\x00R  new.txt\x00old.txt\x00"
	tracked, _, _, _ := parseStatusPorcelainZ(raw)
	if tracked != 2 {
		t.Errorf("拷贝+重命名应各计 1，得到 %d", tracked)
	}
}

func TestParseStatusPorcelainZConflicts(t *testing.T) {
	tracked, untracked, conflicts, _ := parseStatusPorcelainZ("UU both.txt\x00?? u\x00")
	if tracked != 1 || untracked != 1 || conflicts != 1 {
		t.Errorf("期望 1/1/1，得到 %d/%d/%d", tracked, untracked, conflicts)
	}
	for _, code := range []string{"DD", "AU", "UD", "UA", "DU", "AA", "UU"} {
		_, _, c, _ := parseStatusPorcelainZ(code + " f.txt\x00")
		if c != 1 {
			t.Errorf("冲突码 %s 未被识别", code)
		}
	}
}

func TestParseStatusPorcelainZIgnoredAndEmpty(t *testing.T) {
	tracked, untracked, _, ignored := parseStatusPorcelainZ("")
	if tracked != 0 || untracked != 0 || ignored != 0 {
		t.Error("空输入应全为 0")
	}
	_, _, _, ignored = parseStatusPorcelainZ("!! build/\x00M  a.txt\x00")
	if ignored != 1 {
		t.Errorf("ignored 期望 1，得到 %d", ignored)
	}
}

func TestParseLeftRightCount(t *testing.T) {
	l, r, ok := parseLeftRightCount("3\t5\n")
	if !ok || l != 3 || r != 5 {
		t.Errorf("期望 (3,5,true)，得到 (%d,%d,%v)", l, r, ok)
	}
	if _, _, ok := parseLeftRightCount("garbage"); ok {
		t.Error("非法输入应返回 ok=false")
	}
	if _, _, ok := parseLeftRightCount("1 2 3"); ok {
		t.Error("字段数不对应返回 ok=false")
	}
}

func TestShaPtr(t *testing.T) {
	if shaPtr("") != nil {
		t.Error("空串应为 nil")
	}
	if shaPtr(allZeroSHA) != nil {
		t.Error("全零 SHA（unborn）应为 nil")
	}
	if got := shaPtr("abc123"); got == nil || *got != "abc123" {
		t.Error("正常 SHA 应原样返回")
	}
}
