package project

import "os"

// writeFileRaw 原样写字节到文件（测试辅助）。
func writeFileRaw(path string, content []byte) error {
	return os.WriteFile(path, content, 0o644)
}
