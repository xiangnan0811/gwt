package main

// table.go —— 终端表格渲染：东亚宽度计算、截断、加权列宽收缩、着色。

import (
	"strconv"
	"strings"
	"unicode"
)

type runeRange struct {
	lo, hi rune
}

// wideRanges 是 Unicode EastAsianWidth 中 W(ide) + F(ullwidth) 的区间表，
// 用于按「终端显示宽度」对齐中日韩文本。区间表由 Python 3.14 的
// unicodedata（Unicode 16.0.0）导出，避免手抄出错。
var wideRanges = []runeRange{
	{0x1100, 0x115F},
	{0x231A, 0x231B},
	{0x2329, 0x232A},
	{0x23E9, 0x23EC},
	{0x23F0, 0x23F0},
	{0x23F3, 0x23F3},
	{0x25FD, 0x25FE},
	{0x2614, 0x2615},
	{0x2630, 0x2637},
	{0x2648, 0x2653},
	{0x267F, 0x267F},
	{0x268A, 0x268F},
	{0x2693, 0x2693},
	{0x26A1, 0x26A1},
	{0x26AA, 0x26AB},
	{0x26BD, 0x26BE},
	{0x26C4, 0x26C5},
	{0x26CE, 0x26CE},
	{0x26D4, 0x26D4},
	{0x26EA, 0x26EA},
	{0x26F2, 0x26F3},
	{0x26F5, 0x26F5},
	{0x26FA, 0x26FA},
	{0x26FD, 0x26FD},
	{0x2705, 0x2705},
	{0x270A, 0x270B},
	{0x2728, 0x2728},
	{0x274C, 0x274C},
	{0x274E, 0x274E},
	{0x2753, 0x2755},
	{0x2757, 0x2757},
	{0x2795, 0x2797},
	{0x27B0, 0x27B0},
	{0x27BF, 0x27BF},
	{0x2B1B, 0x2B1C},
	{0x2B50, 0x2B50},
	{0x2B55, 0x2B55},
	{0x2E80, 0x2E99},
	{0x2E9B, 0x2EF3},
	{0x2F00, 0x2FD5},
	{0x2FF0, 0x303E},
	{0x3041, 0x3096},
	{0x3099, 0x30FF},
	{0x3105, 0x312F},
	{0x3131, 0x318E},
	{0x3190, 0x31E5},
	{0x31EF, 0x321E},
	{0x3220, 0x3247},
	{0x3250, 0xA48C},
	{0xA490, 0xA4C6},
	{0xA960, 0xA97C},
	{0xAC00, 0xD7A3},
	{0xF900, 0xFAFF},
	{0xFE10, 0xFE19},
	{0xFE30, 0xFE52},
	{0xFE54, 0xFE66},
	{0xFE68, 0xFE6B},
	{0xFF01, 0xFF60},
	{0xFFE0, 0xFFE6},
	{0x16FE0, 0x16FE4},
	{0x16FF0, 0x16FF1},
	{0x17000, 0x187F7},
	{0x18800, 0x18CD5},
	{0x18CFF, 0x18D08},
	{0x1AFF0, 0x1AFF3},
	{0x1AFF5, 0x1AFFB},
	{0x1AFFD, 0x1AFFE},
	{0x1B000, 0x1B122},
	{0x1B132, 0x1B132},
	{0x1B150, 0x1B152},
	{0x1B155, 0x1B155},
	{0x1B164, 0x1B167},
	{0x1B170, 0x1B2FB},
	{0x1D300, 0x1D356},
	{0x1D360, 0x1D376},
	{0x1F004, 0x1F004},
	{0x1F0CF, 0x1F0CF},
	{0x1F18E, 0x1F18E},
	{0x1F191, 0x1F19A},
	{0x1F200, 0x1F202},
	{0x1F210, 0x1F23B},
	{0x1F240, 0x1F248},
	{0x1F250, 0x1F251},
	{0x1F260, 0x1F265},
	{0x1F300, 0x1F320},
	{0x1F32D, 0x1F335},
	{0x1F337, 0x1F37C},
	{0x1F37E, 0x1F393},
	{0x1F3A0, 0x1F3CA},
	{0x1F3CF, 0x1F3D3},
	{0x1F3E0, 0x1F3F0},
	{0x1F3F4, 0x1F3F4},
	{0x1F3F8, 0x1F43E},
	{0x1F440, 0x1F440},
	{0x1F442, 0x1F4FC},
	{0x1F4FF, 0x1F53D},
	{0x1F54B, 0x1F54E},
	{0x1F550, 0x1F567},
	{0x1F57A, 0x1F57A},
	{0x1F595, 0x1F596},
	{0x1F5A4, 0x1F5A4},
	{0x1F5FB, 0x1F64F},
	{0x1F680, 0x1F6C5},
	{0x1F6CC, 0x1F6CC},
	{0x1F6D0, 0x1F6D2},
	{0x1F6D5, 0x1F6D7},
	{0x1F6DC, 0x1F6DF},
	{0x1F6EB, 0x1F6EC},
	{0x1F6F4, 0x1F6FC},
	{0x1F7E0, 0x1F7EB},
	{0x1F7F0, 0x1F7F0},
	{0x1F90C, 0x1F93A},
	{0x1F93C, 0x1F945},
	{0x1F947, 0x1F9FF},
	{0x1FA70, 0x1FA7C},
	{0x1FA80, 0x1FA89},
	{0x1FA8F, 0x1FAC6},
	{0x1FACE, 0x1FADC},
	{0x1FADF, 0x1FAE9},
	{0x1FAF0, 0x1FAF8},
	{0x20000, 0x2FFFD},
	{0x30000, 0x3FFFD},
}

func isWide(r rune) bool {
	lo, hi := 0, len(wideRanges)-1
	for lo <= hi {
		mid := (lo + hi) / 2
		switch {
		case r < wideRanges[mid].lo:
			hi = mid - 1
		case r > wideRanges[mid].hi:
			lo = mid + 1
		default:
			return true
		}
	}
	return false
}

// dwidth 计算字符串的终端显示宽度（东亚宽字符算 2，组合符算 0）。
func dwidth(s string) int {
	w := 0
	for _, r := range s {
		if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || r == 0x200B {
			continue
		}
		if isWide(r) {
			w += 2
		} else {
			w++
		}
	}
	return w
}

// truncate 按显示宽度截断，超宽时用 … 收尾；where="start" 时保留尾部。
func truncate(s string, maxw int, where string) string {
	if maxw <= 0 {
		return ""
	}
	if dwidth(s) <= maxw {
		return s
	}
	if maxw == 1 {
		return "…"
	}
	budget := maxw - 1 // 给省略号留位
	if where == "start" {
		runes := []rune(s)
		acc := 0
		start := len(runes)
		for i := len(runes) - 1; i >= 0; i-- {
			cw := dwidth(string(runes[i]))
			if acc+cw > budget {
				break
			}
			acc += cw
			start = i
		}
		return "…" + string(runes[start:])
	}
	acc := 0
	end := 0
	for i, r := range []rune(s) {
		cw := dwidth(string(r))
		if acc+cw > budget {
			break
		}
		acc += cw
		end = i + 1
	}
	return string([]rune(s)[:end]) + "…"
}

func pad(s string, width int) string {
	fill := width - dwidth(s)
	if fill <= 0 {
		return s
	}
	return s + strings.Repeat(" ", fill)
}

type column struct {
	header string
	cap    int
	min    int
	dir    string // "end" | "start"
	weight float64
}

var columns = []column{
	{"仓库", 26, 6, "end", 1.2},
	{"worktree 路径", 48, 14, "start", 0.6}, // 路径重要，权重最低（最后才压）
	{"分支", 34, 8, "end", 1.6},
	{"改动", 12, 5, "end", 0.5},
	{"未跟踪", 12, 6, "end", 0.5},
	{"upstream", 32, 8, "end", 2.0},
	{"ahead/behind", 13, 11, "end", 3.0},
	{"标记", 34, 10, "end", 3.0},
}

type tableRow struct {
	cells []string
	wt    *Worktree
}

func buildRows(repos []*Repo) []tableRow {
	var rows []tableRow
	for _, repo := range repos {
		for _, wt := range repo.worktrees {
			name := repo.name
			if wt.isMain && !wt.bare {
				name += " [main]"
			}
			flags := make([]string, 0, len(wt.problems))
			for _, p := range wt.problems {
				if problemRedundantInTable[p] {
					continue
				}
				if short, ok := problemShort[p]; ok {
					flags = append(flags, short)
				} else {
					flags = append(flags, p)
				}
			}
			cells := []string{
				name,
				wt.path,
				wt.branchDisplay(),
				dashIfNil(wt.tracked),
				dashIfNil(wt.untracked),
				wt.upstreamDisplay(),
				wt.aheadBehindDisplay(),
				strings.Join(flags, ","),
			}
			rows = append(rows, tableRow{cells: cells, wt: wt})
		}
	}
	return rows
}

func dashIfNil(v *int) string {
	if v == nil {
		return "-"
	}
	return strconv.Itoa(*v)
}

// fitWidths 返回 (每列宽度, 是否有列被截断)。
func fitWidths(rows []tableRow, termWidth int, noTruncate bool) ([]int, bool) {
	ncol := len(columns)
	widths := make([]int, ncol)
	for i, c := range columns {
		w := dwidth(c.header)
		for _, r := range rows {
			if cw := dwidth(r.cells[i]); cw > w {
				w = cw
			}
		}
		if !noTruncate {
			if w > c.cap {
				w = c.cap
			}
			if w < c.min {
				w = c.min
			}
		}
		widths[i] = w
	}

	if noTruncate {
		return widths, false
	}

	avail := termWidth - 2*(ncol-1)
	total := 0
	for _, w := range widths {
		total += w
	}
	guard := 0
	for total > avail && guard < 200000 {
		guard++
		// 按 slack*权重 选列压缩：权重高的（upstream/标记）先让位，路径最后
		best := -1
		bestKey := 0.0
		bestSlack := 0
		for i, c := range columns {
			slack := widths[i] - c.min
			if slack <= 0 {
				continue
			}
			key := float64(slack) * c.weight
			if best < 0 || key > bestKey || (key == bestKey && slack > bestSlack) {
				best, bestKey, bestSlack = i, key, slack
			}
		}
		if best < 0 {
			break
		}
		widths[best]--
		total--
	}

	truncated := false
	for i, c := range columns {
		if dwidth(c.header) > widths[i] {
			truncated = true
		}
		for _, r := range rows {
			if dwidth(r.cells[i]) > widths[i] {
				truncated = true
			}
		}
	}
	return widths, truncated
}

type palette struct{ enabled bool }

func (p palette) wrap(code, s string) string {
	if !p.enabled {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (p palette) bold(s string) string   { return p.wrap("1", s) }
func (p palette) dim(s string) string    { return p.wrap("2", s) }
func (p palette) red(s string) string    { return p.wrap("31", s) }
func (p palette) yellow(s string) string { return p.wrap("33", s) }

// renderTable 返回表格行与「是否有列被截断」。
func renderTable(repos []*Repo, termWidth int, noTruncate bool, pal palette) ([]string, bool) {
	rows := buildRows(repos)
	widths, truncated := fitWidths(rows, termWidth, noTruncate)

	headers := make([]string, len(columns))
	for i, c := range columns {
		headers[i] = pad(truncate(c.header, widths[i], "end"), widths[i])
	}
	lines := []string{pal.bold(strings.Join(headers, "  "))}

	seps := make([]string, len(columns))
	for i := range columns {
		seps[i] = strings.Repeat("-", widths[i])
	}
	lines = append(lines, pal.dim(strings.Join(seps, "  ")))

	for _, row := range rows {
		parts := make([]string, len(columns))
		severe := false
		for _, p := range row.wt.problems {
			if severeProblems[p] {
				severe = true
				break
			}
		}
		for i, c := range columns {
			plain := truncate(row.cells[i], widths[i], c.dir)
			padded := pad(plain, widths[i])
			if i == len(columns)-1 && plain != "" && pal.enabled {
				colored := pal.yellow(plain)
				if severe {
					colored = pal.red(plain)
				}
				padded = colored + strings.Repeat(" ", max(0, widths[i]-dwidth(plain)))
			}
			parts[i] = padded
		}
		lines = append(lines, strings.TrimRight(strings.Join(parts, "  "), " "))
	}
	return lines, truncated
}
