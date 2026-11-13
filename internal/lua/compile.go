package lua

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
)

// 编辑器诊断用的错误解析（PRD §F4「保存时 Go 侧编译校验，错误行号回显编辑器」）。
//
// gopher-lua 的错误文本形如：
//
//	main.lua line:12(column:5) near 'then': unexpected symbol
//	<name> line:3(column:1): attempt to index a nil value
//
// 行号是编辑器定位的唯一依据，缺失时返回 0 由前端退化为「整文件级错误」。

var (
	reLineCol     = regexp.MustCompile(`line:(\d+)(?:\(column:(\d+)\))?`)
	reNameLineCol = regexp.MustCompile(`:(\d+):(\d+):`) // "chunk:3:5: msg"
	// 运行期错误是 "rt.lua:3: attempt to ..."，没有 column
	reNameLine = regexp.MustCompile(`:(\d+): `)
)

// Diagnostic 一条编辑器诊断。
type Diagnostic struct {
	Line    int    `json:"line"`    // 1-based，0 表示无法定位
	Column  int    `json:"column"`  // 1-based，0 表示未知
	Message string `json:"message"` // 已去掉 "lua: compile xxx:" 前缀
	Level   string `json:"level"`   // error | warning
}

// DiagnosticFromError 把脚本错误转成编辑器诊断（始终返回一条）。
func DiagnosticFromError(name string, err error) Diagnostic {
	if err == nil {
		return Diagnostic{}
	}
	msg := err.Error()
	line, col := ErrorLine(err)
	return Diagnostic{
		Line:    line,
		Column:  col,
		Message: trimScriptPrefix(msg, name),
		Level:   "error",
	}
}

// ErrorLine 从错误中提取 1-based 行号/列号；提取不到返回 0。
func ErrorLine(err error) (line, col int) {
	if err == nil {
		return 0, 0
	}
	return parseLineCol(err.Error())
}

func parseLineCol(msg string) (int, int) {
	if m := reLineCol.FindStringSubmatch(msg); m != nil {
		line, _ := strconv.Atoi(m[1])
		col := 0
		if m[2] != "" {
			col, _ = strconv.Atoi(m[2])
		}
		return line, col
	}
	if m := reNameLineCol.FindStringSubmatch(msg); m != nil {
		line, _ := strconv.Atoi(m[1])
		col, _ := strconv.Atoi(m[2])
		return line, col
	}
	if m := reNameLine.FindStringSubmatch(msg); m != nil {
		line, _ := strconv.Atoi(m[1])
		return line, 0
	}
	return 0, 0
}

// trimScriptPrefix 去掉 "lua: compile <name>: <name> line:..." 里的重复前缀，
// 编辑器里只需要剩下的那段人话。
func trimScriptPrefix(msg, name string) string {
	s := msg
	for _, prefix := range []string{
		"lua: compile " + name + ": ",
		"lua: on_frame: ",
		"lua: detect_frame: ",
		"lua: encode_command: ",
		"lua: run " + name + ": ",
	} {
		s = strings.TrimPrefix(s, prefix)
	}
	s = strings.TrimPrefix(s, name+" ")
	return strings.TrimSpace(s)
}

// ErrNoOnFrame 脚本未定义 on_frame（试运行的常见「错误」而非崩溃）。
var ErrNoOnFrame = errors.New("lua: script has no on_frame")

// Compile 只做编译校验，不碰运行中的脚本（编辑器保存前验证 / 试运行）。
func Compile(name string, src []byte) error {
	_, err := compileScript(name, src)
	return err
}
