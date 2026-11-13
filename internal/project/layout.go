package project

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed templates/blank/main.lua templates/blank/charts.json
var blankTemplate embed.FS

// Scaffold 在 dir 生成工程目录骨架（PRD §3.2）+ 空白模板，
// 并写入 project.json。已存在 project.json 时拒绝，不覆盖。
func Scaffold(dir, name, description string) (*ProjectMeta, error) {
	if IsProject(dir) {
		return nil, fmt.Errorf("%s already contains a project.json", dir)
	}
	_, statErr := os.Stat(dir)
	dirExisted := statErr == nil

	meta := DefaultMeta(name)
	meta.Description = description

	meta, err := scaffoldInto(dir, meta)
	if err != nil && !dirExisted {
		// 只清理本次新建的目录，绝不删除用户已有目录
		_ = os.RemoveAll(dir)
	}
	return meta, err
}

func scaffoldInto(dir string, meta *ProjectMeta) (*ProjectMeta, error) {

	dirs := []string{
		filepath.Join(dir, "scripts"),
		filepath.Join(dir, "conversations"),
		filepath.Join(dir, "data"),
		filepath.Join(dir, ".seriallens", "samples"),
		filepath.Join(dir, ".seriallens", "drafts"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	files := map[string]string{
		filepath.Join(dir, "scripts", "main.lua"): "templates/blank/main.lua",
		filepath.Join(dir, "charts.json"):         "templates/blank/charts.json",
	}
	for dst, src := range files {
		content, err := blankTemplate.ReadFile(src)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(dst, content, 0o644); err != nil {
			return nil, err
		}
	}
	if err := SaveMeta(dir, meta); err != nil {
		return nil, err
	}
	return meta, nil
}
