package main

// term.go —— 不依赖第三方库的终端判断与宽度探测。
// 只用标准库 syscall/unsafe（Linux ioctl）。

import (
	"os"
	"strconv"
	"syscall"
	"unsafe"
)

type winsize struct {
	Row, Col, Xpixel, Ypixel uint16
}

// isTerminal 用 TCGETS ioctl 判断是否真的是终端。
// 不用 os.ModeCharDevice：/dev/null 也是字符设备，会被误判成 tty。
func isTerminal(f *os.File) bool {
	var t syscall.Termios
	_, _, errno := syscall.Syscall6(
		syscall.SYS_IOCTL,
		f.Fd(),
		uintptr(syscall.TCGETS),
		uintptr(unsafe.Pointer(&t)),
		0, 0, 0,
	)
	return errno == 0
}

// termWidth 行为与 Python shutil.get_terminal_size 对齐：
// 先看 COLUMNS 环境变量，再问 tty，最后兜底 120。
func termWidth() int {
	if s := os.Getenv("COLUMNS"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return n
		}
	}
	var ws winsize
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		os.Stdout.Fd(),
		uintptr(syscall.TIOCGWINSZ),
		uintptr(unsafe.Pointer(&ws)),
	)
	if errno == 0 && ws.Col > 0 {
		return int(ws.Col)
	}
	return 120
}
