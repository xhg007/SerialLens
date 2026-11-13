package app

import (
	"os"
	"path/filepath"
	"testing"

	"seriallens/internal/project"
)

// 目录选择器选完后的探测三分支：已有工程 / 空目录 / 不存在。
// UI 依赖它给出准确提示，而不是把「选错目录」抛成 read project.json 失败。
func TestInspectProjectDir(t *testing.T) {
	root := t.TempDir()
	svc := NewProjectService()

	meta, err := project.Scaffold(filepath.Join(root, "demo"), "demo", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = meta

	empty := filepath.Join(root, "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name      string
		dir       string
		exists    bool
		isProject bool
		wantName  string
	}{
		{"project dir", filepath.Join(root, "demo"), true, true, "demo"},
		{"empty dir", empty, true, false, ""},
		{"missing dir", filepath.Join(root, "nope"), false, false, ""},
		// 普通文件按「不可用目录」处理：exists 语义 = 可作为工程目录
		{"file instead of dir", filepath.Join(root, "demo", "project.json"), false, false, ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := svc.InspectProjectDir(c.dir)
			if got["exists"] != c.exists {
				t.Fatalf("exists = %v, want %v (%+v)", got["exists"], c.exists, got)
			}
			if got["is_project"] != c.isProject {
				t.Fatalf("is_project = %v, want %v (%+v)", got["is_project"], c.isProject, got)
			}
			if got["name"] != c.wantName {
				t.Fatalf("name = %v, want %q (%+v)", got["name"], c.wantName, got)
			}
		})
	}
}
