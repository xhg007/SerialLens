package lua

import (
	"strings"
	"testing"
)

// 语法错误必须能定位到行号——编辑器靠它放红波浪线（PRD F4）。
func TestDiagnosticFromCompileError(t *testing.T) {
	src := []byte("function on_frame(raw)\n  local a = 1\n  return a ++\nend\n")
	err := Compile("main.lua", src)
	if err == nil {
		t.Fatal("expected compile error")
	}
	d := DiagnosticFromError("main.lua", err)
	if d.Line != 3 {
		t.Fatalf("line = %d, want 3 (msg=%q)", d.Line, d.Message)
	}
	if strings.Contains(d.Message, "lua: compile") {
		t.Fatalf("message still has internal prefix: %q", d.Message)
	}
	if d.Message == "" || d.Level != "error" {
		t.Fatalf("bad diagnostic: %+v", d)
	}
}

// 运行期错误同样要能定位（script:error 事件里的 line 字段）。
func TestErrorLineFromRuntimeError(t *testing.T) {
	rt := New(Options{})
	defer rt.Close()
	if err := rt.LoadScript("rt.lua", []byte("function on_frame(raw)\n  local x = nil\n  return x.field\nend\n")); err != nil {
		t.Fatal(err)
	}
	_, _, err := rt.CallOnFrame([]byte{1})
	if err == nil {
		t.Fatal("expected runtime error")
	}
	line, _ := ErrorLine(err)
	if line != 3 {
		t.Fatalf("line = %d, want 3 (err=%v)", line, err)
	}
}

func TestParseLineColVariants(t *testing.T) {
	cases := []struct {
		msg  string
		line int
		col  int
	}{
		{"main.lua line:12(column:5) near 'then': unexpected symbol", 12, 5},
		{"main.lua line:7: attempt to call a nil value", 7, 0},
		{"chunk:3:5: unexpected symbol", 3, 5},
		{"no line info here", 0, 0},
	}
	for _, c := range cases {
		line, col := parseLineCol(c.msg)
		if line != c.line || col != c.col {
			t.Errorf("parseLineCol(%q) = (%d,%d), want (%d,%d)", c.msg, line, col, c.line, c.col)
		}
	}
}

// manifest 是前端补全的唯一来源，必须与实际注册的全局函数一致，
// 否则补全会出现「提示了但运行时报 nil」。
func TestManifestMatchesRegisteredGlobals(t *testing.T) {
	L := newSandboxedState(Sinks{})
	defer L.Close()

	skip := map[string]bool{"on_frame": true, "detect_frame": true, "encode_command": true} // 回调由脚本定义
	for _, doc := range APIManifest() {
		if skip[doc.Name] {
			continue
		}
		if L.GetGlobal(doc.Name) == nil {
			t.Errorf("manifest 声明了 %s，但运行时没有该全局", doc.Name)
		}
		if doc.Call == "" || doc.Doc == "" {
			t.Errorf("%s 缺少 Call/Doc", doc.Name)
		}
	}
}
