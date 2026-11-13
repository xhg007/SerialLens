package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const recentFileName = "recent_projects.json"
const recentCap = 20

// RecentEntry 最近工程列表条目（PRD §3.4）。
type RecentEntry struct {
	Path       string    `json:"path"`
	Name       string    `json:"name"`
	LastOpened time.Time `json:"last_opened"`
	Pinned     bool      `json:"pinned"`
	// Missing 非持久化字段：Validate 标记失效（路径不存在或缺 project.json）。
	Missing bool `json:"-"`
}

type recentFile struct {
	Projects []RecentEntry `json:"projects"`
}

// RecentStore 最近工程列表存取，根目录默认 ~/.seriallens（可注入便于测试）。
type RecentStore struct {
	home    string
	missing map[string]bool // Validate 标记的失效路径（不持久化）
	mu      sync.Mutex
}

// NewRecentStore 创建 store；home 为空时用 ~/.seriallens。
func NewRecentStore(home string) *RecentStore {
	if home == "" {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			home = filepath.Join(homeDir, ".seriallens")
		}
	}
	return &RecentStore{home: home, missing: map[string]bool{}}
}

func (r *RecentStore) filePath() string {
	return filepath.Join(r.home, recentFileName)
}

// List 返回条目：last_opened 倒序，pinned 恒在前。
func (r *RecentStore) List() []RecentEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.listLocked()
}

func (r *RecentStore) listLocked() []RecentEntry {
	raw, err := os.ReadFile(r.filePath())
	if err != nil {
		return nil
	}
	var doc recentFile
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil // 损坏文件视为空列表，不 panic
	}
	list := doc.Projects
	for i := range list {
		if r.missing[list[i].Path] {
			list[i].Missing = true
		}
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Pinned != list[j].Pinned {
			return list[i].Pinned
		}
		return list[i].LastOpened.After(list[j].LastOpened)
	})
	return list
}

// RecordOpen 记录/刷新一次打开，超上限时淘汰最旧的未 pinned 条目。
func (r *RecentStore) RecordOpen(path, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	list := r.listLocked()
	now := time.Now()
	found := false
	for i := range list {
		if list[i].Path == path {
			list[i].LastOpened = now
			if name != "" {
				list[i].Name = name
			}
			found = true
			break
		}
	}
	if !found {
		list = append(list, RecentEntry{Path: path, Name: name, LastOpened: now})
	}
	// 超上限：从最旧的非 pinned 开始淘汰
	if len(list) > recentCap {
		kept := make([]RecentEntry, 0, recentCap)
		// pinned 全保留，其余按时间新→旧保留至满
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].Pinned != list[j].Pinned {
				return list[i].Pinned
			}
			return list[i].LastOpened.After(list[j].LastOpened)
		})
		for _, e := range list {
			if len(kept) < recentCap || e.Pinned {
				kept = append(kept, e)
			}
		}
		list = kept
	}
	return r.saveLocked(list)
}

// Remove 删除条目。
func (r *RecentStore) Remove(path string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	list := r.listLocked()
	kept := list[:0]
	for _, e := range list {
		if e.Path != path {
			kept = append(kept, e)
		}
	}
	return r.saveLocked(kept)
}

// SetPinned 设置置顶。
func (r *RecentStore) SetPinned(path string, pinned bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	list := r.listLocked()
	for i := range list {
		if list[i].Path == path {
			list[i].Pinned = pinned
			break
		}
	}
	return r.saveLocked(list)
}

// Validate 标记失效条目（不删除）。Missing 标记在内存中，List 会带出。
func (r *RecentStore) Validate() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.missing = map[string]bool{}
	for _, e := range r.listLocked() {
		if !IsProject(e.Path) {
			r.missing[e.Path] = true
		}
	}
}

func (r *RecentStore) saveLocked(list []RecentEntry) error {
	if err := os.MkdirAll(r.home, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(recentFile{Projects: list}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(r.filePath(), raw, 0o644)
}
