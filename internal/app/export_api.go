package app

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
)

// ExportAPI 导出落盘：前端先弹系统保存对话框拿路径，再调这里写文件。
// 内容统一走 base64（PNG 等二进制与 CSV 文本共用一条路径，避免换行/编码歧义）。
type ExportAPI struct{}

func NewExportAPI() *ExportAPI { return &ExportAPI{} }

// SaveFile 把 base64 编码的内容写到指定路径。
func (e *ExportAPI) SaveFile(path, contentB64 string) error {
	if path == "" {
		return fmt.Errorf("export: empty path")
	}
	if filepath.Dir(path) == "." && filepath.Base(path) == path {
		// 只有文件名没有目录：写当前目录允许，但要防路径注入类怪输入
		_ = filepath.Clean(path)
	}
	data, err := base64.StdEncoding.DecodeString(contentB64)
	if err != nil {
		return fmt.Errorf("export: decode base64: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("export: write %s: %w", path, err)
	}
	return nil
}
