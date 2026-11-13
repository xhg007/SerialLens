package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"seriallens/internal/lua"
)

var idSeq uint64

// newID 生成短事件 id（confirm/tool call 用，不需要全局唯一性保证）。
func newID(prefix string) string {
	n := atomic.AddUint64(&idSeq, 1)
	return fmt.Sprintf("%s_%d_%d", prefix, time.Now().UnixNano(), n)
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

// diagFromError 复用 lua 包的错误解析（行号/列号/消息）。
func diagFromError(name string, err error) lua.Diagnostic {
	return lua.DiagnosticFromError(name, err)
}

// unifiedDiff 生成统一的 -/+ diff（LCS）。
//
// 只用于「给人看的确认卡片」与工具结果回喂；前端 DiffEditor 会用
// original/modified 自己算更漂亮的 diff。
func unifiedDiff(oldName, oldSrc, newName, newSrc string) string {
	a := splitLines(oldSrc)
	b := splitLines(newSrc)
	ops := diffLines(a, b)

	var out strings.Builder
	out.WriteString("--- " + oldName + "\n+++ " + newName + "\n")
	for _, op := range ops {
		out.WriteString(op.prefix)
		out.WriteString(op.line)
		out.WriteString("\n")
	}
	s := out.String()
	// 卡片里塞太长没意义：截断到 8KB
	const maxLen = 8 << 10
	if len(s) > maxLen {
		return s[:maxLen] + "\n…（diff 已截断）\n"
	}
	return s
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}

type diffOp struct {
	prefix string
	line   string
}

// diffLines 经典 LCS 动态规划；脚本行数很小（<1k），O(n*m) 可以接受。
func diffLines(a, b []string) []diffOp {
	n, m := len(a), len(b)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	ops := make([]diffOp, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{" ", a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, diffOp{"-", a[i]})
			i++
		default:
			ops = append(ops, diffOp{"+", b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{"-", a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{"+", b[j]})
	}
	return ops
}
