package agent

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// 样本来源：工程内 .seriallens/samples/（PRD §3.2）。
//
// 支持的格式刻意保持极简——样本是给协议解码用的「原始字节」：
//   - *.hex / *.txt：HEX 文本（允许空格、逗号、0x 前缀、换行）
//   - *.bin：裸字节
//
// 目前没有「捕获到样本」的 UI（属 F7 录制/样本），所以文件由用户手动放入，
// 或由后续录制功能写入；工具层只负责读出。
const (
	samplesDir    = ".seriallens/samples"
	recordingsDir = "data"
	maxSampleSize = 16 << 10 // 单样本 16KB 上限，避免模型上下文被撑爆
)

// SampleStore 读取工程内样本目录。
type SampleStore struct {
	dir string // 工程根目录
}

// NewSampleStore 绑定工程目录。
func NewSampleStore(projectDir string) *SampleStore {
	return &SampleStore{dir: projectDir}
}

// List 列出样本（按修改时间倒序）。
func (s *SampleStore) List(limit int) ([]SampleMeta, error) {
	if s.dir == "" {
		return nil, fmt.Errorf("no project open")
	}
	entries, err := os.ReadDir(filepath.Join(s.dir, samplesDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]SampleMeta, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext != ".hex" && ext != ".txt" && ext != ".bin" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		raw, err := s.read(e.Name())
		if err != nil {
			continue
		}
		preview := raw
		if len(preview) > 24 {
			preview = preview[:24]
		}
		out = append(out, SampleMeta{
			ID:      e.Name(),
			Name:    e.Name(),
			Size:    len(raw),
			ModTime: info.ModTime().UnixMilli(),
			Preview: strings.ToUpper(hex.EncodeToString(preview)),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModTime > out[j].ModTime })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Read 读取样本原始字节。
func (s *SampleStore) Read(id string) ([]byte, error) {
	if s.dir == "" {
		return nil, fmt.Errorf("no project open")
	}
	if err := safeName(id); err != nil {
		return nil, err
	}
	return s.read(id)
}

func (s *SampleStore) read(id string) ([]byte, error) {
	path := filepath.Join(s.dir, samplesDir, id)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) > maxSampleSize {
		raw = raw[:maxSampleSize]
	}
	if strings.EqualFold(filepath.Ext(id), ".bin") {
		return raw, nil
	}
	return parseHexText(string(raw))
}

// Recordings 列出 data/ 下的 .srec 录制。
func (s *SampleStore) Recordings() ([]RecordingMeta, error) {
	if s.dir == "" {
		return nil, fmt.Errorf("no project open")
	}
	entries, err := os.ReadDir(filepath.Join(s.dir, recordingsDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]RecordingMeta, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".srec") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		meta := RecordingMeta{File: e.Name(), Size: info.Size()}
		if hdr, err := readSrecHeader(filepath.Join(s.dir, recordingsDir, e.Name())); err == nil {
			meta.Port, meta.Baudrate, meta.Samples, meta.CreatedAt = hdr.Port, hdr.Baudrate, hdr.Samples, hdr.CreatedAt
		} else {
			meta.Note = "header unreadable: " + err.Error()
		}
		out = append(out, meta)
	}
	return out, nil
}

// parseHexText 容忍空格/逗号/换行/0x 前缀/注释行。
func parseHexText(s string) ([]byte, error) {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		line = strings.TrimPrefix(line, "0x")
		line = strings.TrimPrefix(line, "0X")
		for _, r := range line {
			switch {
			case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
				b.WriteRune(r)
			case r == ' ', r == ',', r == '-', r == '\t', r == '\r':
				// 分隔符
			default:
				return nil, fmt.Errorf("invalid hex character %q", r)
			}
		}
	}
	digits := b.String()
	if len(digits)%2 != 0 {
		return nil, fmt.Errorf("hex has odd number of digits (%d)", len(digits))
	}
	if digits == "" {
		return nil, fmt.Errorf("empty sample")
	}
	return hex.DecodeString(digits)
}

// safeName 只允许同目录下的简单文件名（拒绝路径穿越）。
func safeName(name string) error {
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return fmt.Errorf("invalid name %q", name)
	}
	return nil
}

// srecHeader .srec 文件头（PRD §3.7，64 字节）。
type srecHeader struct {
	Port      string
	Baudrate  int
	Samples   int
	CreatedAt string
}

func readSrecHeader(path string) (*srecHeader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	buf := make([]byte, 64)
	n, err := f.Read(buf)
	if err != nil || n < 64 {
		return nil, fmt.Errorf("short header (%d bytes)", n)
	}
	if string(buf[:8]) != "SLREC001" {
		return nil, fmt.Errorf("bad magic %q", string(buf[:8]))
	}
	le := func(b []byte) int {
		v := 0
		for i := len(b) - 1; i >= 0; i-- {
			v = v<<8 | int(b[i])
		}
		return v
	}
	port := strings.TrimRight(string(buf[20:52]), "\x00 ")
	return &srecHeader{
		Port:      port,
		Baudrate:  le(buf[52:56]),
		Samples:   le(buf[56:60]),
		CreatedAt: fmt.Sprintf("%d", int64(le(buf[12:20]))),
	}, nil
}
