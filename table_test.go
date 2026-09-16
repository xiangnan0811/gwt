package main

import (
	"strings"
	"testing"
)

func TestDwidth(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"abc", 3},
		{"", 0},
		{"中文", 4},
		{"仓库", 4},
		{"a中b", 4},
		{"日本語", 6},
		{"한글", 4},
		{"Ａ", 2},        // 全角 U+FF21
		{"！", 2},        // 全角标点 U+FF01
		{"…", 1},        // 省略号是 Ambiguous → 1
		{"a b", 3},      // worktree 路径里的空格
		{"特性/一", 7},     // 中 2+2 + / 1 + 一 2
		{"🎉", 2},        // emoji
		{"e\u0301", 1},  // e + 组合重音 → 组合符宽度 0
		{"\u200b", 0},   // 零宽空格
		{"αβγ", 3},      // 希腊字母窄
		{"한글abc中文", 11}, // 한글4 + abc3 + 中文4
	}
	for _, c := range cases {
		if got := dwidth(c.in); got != c.want {
			t.Errorf("dwidth(%q) = %d，期望 %d", c.in, got, c.want)
		}
	}
}

func TestIsWide(t *testing.T) {
	for _, r := range []rune{'中', '倉', 'あ', 'ア', '한', 'Ａ', '！', '🎉'} {
		if !isWide(r) {
			t.Errorf("%q (U+%04X) 应判为宽字符", r, r)
		}
	}
	for _, r := range []rune{'a', 'Z', '0', ' ', '/', '-', '…', 'α', 'é'} {
		if isWide(r) {
			t.Errorf("%q (U+%04X) 不应判为宽字符", r, r)
		}
	}
}

func TestTruncateStartKeepsTail(t *testing.T) {
	out := truncate("/a/very/long/path/tail", 10, "start")
	if dwidth(out) != 10 {
		t.Errorf("宽度期望 10，得到 %d (%q)", dwidth(out), out)
	}
	if !strings.HasSuffix(out, "path/tail") {
		t.Errorf("应保留尾部，得到 %q", out)
	}
	if !strings.HasPrefix(out, "…") {
		t.Errorf("应以 … 开头，得到 %q", out)
	}
}

func TestTruncateEndKeepsHead(t *testing.T) {
	out := truncate("origin/feature/x", 10, "end")
	if dwidth(out) != 10 {
		t.Errorf("宽度期望 10，得到 %d (%q)", dwidth(out), out)
	}
	if !strings.HasSuffix(out, "…") {
		t.Errorf("应以 … 结尾，得到 %q", out)
	}
	if !strings.HasPrefix(out, "origin/fe") {
		t.Errorf("应保留头部，得到 %q", out)
	}
}

func TestTruncateNoOpWhenFits(t *testing.T) {
	for _, s := range []string{"", "abc", "中文仓库"} {
		if got := truncate(s, 20, "end"); got != s {
			t.Errorf("truncate(%q, 20) 不该改动，得到 %q", s, got)
		}
		if got := truncate(s, 20, "start"); got != s {
			t.Errorf("truncate(%q, 20, start) 不该改动，得到 %q", s, got)
		}
	}
}

func TestTruncateWideCharsRespectWidth(t *testing.T) {
	// 纯中文串按显示宽度截断，不能出现半个字
	out := truncate("中文仓库名字很长", 7, "end")
	if dwidth(out) > 7 {
		t.Errorf("截断后宽度 %d 超过 7: %q", dwidth(out), out)
	}
	out = truncate("中文仓库名字很长", 7, "start")
	if dwidth(out) > 7 {
		t.Errorf("左截断后宽度 %d 超过 7: %q", dwidth(out), out)
	}
}

func TestTruncateDegenerateWidths(t *testing.T) {
	if got := truncate("abc", 0, "end"); got != "" {
		t.Errorf("宽度 0 应为空串，得到 %q", got)
	}
	if got := truncate("abc", 1, "end"); got != "…" {
		t.Errorf("宽度 1 应为 …，得到 %q", got)
	}
	if got := truncate("中文", 2, "end"); dwidth(got) > 2 {
		t.Errorf("宽度超限: %q", got)
	}
}

func TestPadUsesDisplayWidth(t *testing.T) {
	if got := pad("中文", 6); got != "中文  " {
		t.Errorf("pad(\"中文\", 6) = %q，期望 \"中文  \"", got)
	}
	if got := pad("abcdef", 3); got != "abcdef" {
		t.Errorf("超宽不应截断，得到 %q", got)
	}
}

func TestFitWidthsShrinksPathLast(t *testing.T) {
	// 构造一个很长的路径行：窄终端下应优先牺牲 upstream/分支/标记，而不是路径
	path := "/home/someone/very/long/path/to/a/worktree"
	mk := func() []tableRow {
		return []tableRow{{cells: []string{
			"repo", path, "feature/some-long-branch",
			"3", "4", "origin/feature/some-long-branch", "+1/-2", "路径失效,待清理",
		}, wt: &Worktree{problems: []string{"path_missing", "prunable"}}}}
	}
	lineWidth := func(widths []int) int {
		total := 2 * (len(columns) - 1)
		for _, w := range widths {
			total += w
		}
		return total
	}

	// 100 列：应当能完整放下（总宽度 <= 100），且路径没有被压到最小值
	widths, _ := fitWidths(mk(), 100, false)
	if got := lineWidth(widths); got > 100 {
		t.Errorf("100 列下总宽度 %d 超过 100", got)
	}
	if widths[1] <= columns[1].min {
		t.Errorf("路径列被压到最小值 %d，说明收缩策略没给路径留空间", widths[1])
	}
	// 路径应当比 upstream/分支 这两列更宽（权重更低 = 更晚被压）
	if widths[1] <= widths[5] {
		t.Errorf("路径列 %d 不应比 upstream 列 %d 窄", widths[1], widths[5])
	}
	// 每列都不小于自己的最小值
	for i, c := range columns {
		if widths[i] < c.min {
			t.Errorf("第 %d 列(%s) 宽度 %d 小于最小值 %d", i, c.header, widths[i], c.min)
		}
	}

	// 80 列：各列最小宽度之和(68) + 分隔符(14) = 82 > 80，
	// 此时允许溢出（不把任何列压到最小值以下），但溢出量必须有界。
	widths80, truncated := fitWidths(mk(), 80, false)
	if !truncated {
		t.Error("80 列下应当报告被截断")
	}
	if got := lineWidth(widths80); got > 82 {
		t.Errorf("80 列下总宽度 %d 超过硬最小值下界 82", got)
	}
	for i, c := range columns {
		if widths80[i] < c.min {
			t.Errorf("80 列下第 %d 列(%s) 被压到最小值以下: %d < %d", i, c.header, widths80[i], c.min)
		}
	}
}

func TestFitWidthsNoTruncate(t *testing.T) {
	long := strings.Repeat("/x", 60)
	rows := []tableRow{{cells: []string{"r", long, "b", "1", "2", "u", "0/0", ""}, wt: &Worktree{}}}
	widths, truncated := fitWidths(rows, 40, true)
	if truncated {
		t.Error("--no-truncate 时不应报告截断")
	}
	if widths[1] != dwidth(long) {
		t.Errorf("--no-truncate 时路径列应完整，得到 %d，期望 %d", widths[1], dwidth(long))
	}
}

func TestFitWidthsHandlesEmptyRows(t *testing.T) {
	widths, truncated := fitWidths(nil, 120, false)
	if len(widths) != len(columns) {
		t.Fatalf("列数不对: %d", len(widths))
	}
	if truncated {
		t.Error("无数据时不应报告截断")
	}
	for i, c := range columns {
		if widths[i] < dwidth(c.header) {
			t.Errorf("第 %d 列比表头还窄", i)
		}
	}
}

func TestHardMinFloorIs82Columns(t *testing.T) {
	// README「已知限制 9」里的数字要由测试钉住：
	// 8 列最小宽度之和 + 7 个分隔符(每个 2 列) 就是表格能收到的最窄宽度。
	sum := 0
	for _, c := range columns {
		sum += c.min
	}
	floor := sum + 2*(len(columns)-1)
	if floor != 82 {
		t.Fatalf("硬最小值下界应为 82，实际 %d；请同步更新 README 已知限制 9", floor)
	}
	// 比下界窄的请求不应把任何列压到最小值以下
	rows := []tableRow{{cells: []string{
		"/very/long/path/to/worktree/name", "a/very/long/branch/name", "repo",
		"12", "34", "origin/a/very/long/branch", "+10/-20", "路径失效,待清理",
	}, wt: &Worktree{problems: []string{"path_missing", "prunable"}}}}
	for _, req := range []int{40, 60, 80, 81} {
		widths, _ := fitWidths(rows, req, false)
		got := 2 * (len(columns) - 1)
		for i, c := range columns {
			if widths[i] < c.min {
				t.Errorf("宽度 %d 时第 %d 列(%s) 被压到最小值以下: %d < %d",
					req, i, c.header, widths[i], c.min)
			}
			got += widths[i]
		}
		if got != floor {
			t.Errorf("宽度 %d 时总宽度应为下界 %d，实际 %d", req, floor, got)
		}
	}
	// 恰好等于下界时必须放下，且路径列拿到额外空间
	widths, _ := fitWidths(rows, floor, false)
	got := 2 * (len(columns) - 1)
	for _, w := range widths {
		got += w
	}
	if got > floor {
		t.Errorf("宽度 %d 时总宽度 %d 超出请求值", floor, got)
	}
}
