package app

import (
	"context"
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"

	"seriallens/internal/events"
	"seriallens/internal/project"
)

// ProjectService Wails 薄壳：工程域方法绑定 + 事件桥接。
// 业务逻辑全部在 internal/project。
type ProjectService struct {
	svc     *project.Service
	recents *project.RecentStore
	pipe    *Services
}

func NewProjectService() *ProjectService {
	return newProjectService(nil)
}

func newProjectService(pipe *Services) *ProjectService {
	recents := project.NewRecentStore("")
	return &ProjectService{
		recents: recents,
		svc:     project.NewService(recents, nil),
		pipe:    pipe,
	}
}

// ServiceStartup 在应用启动时注入事件桥接。
func (p *ProjectService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	app := application.Get()
	if app == nil {
		return nil
	}
	p.svc.SetEmitter(func(event string, payload map[string]any) {
		app.Event.Emit(event, payload)
	})
	if p.pipe != nil {
		p.pipe.install(app)
	}
	return nil
}

func (p *ProjectService) CreateProject(dir, name, description string) error {
	err := p.svc.Create(dir, name, description)
	if err == nil {
		p.emitRecents()
	}
	return err
}

func (p *ProjectService) OpenProject(dir string) error {
	err := p.svc.Open(dir)
	if err == nil {
		p.emitRecents()
	}
	return err
}

func (p *ProjectService) CloseProject() error {
	return p.svc.Close()
}

// GetCurrentProject 返回 {ok:false} 或 {ok:true, path, meta}。
func (p *ProjectService) GetCurrentProject() map[string]any {
	dir, meta, ok := p.svc.Current()
	if !ok {
		return map[string]any{"ok": false}
	}
	return map[string]any{"ok": true, "path": dir, "meta": meta}
}

// InspectProjectDir 探测目录状态，供系统文件选择器选完目录后即时反馈。
//
// 直接调用 OpenProject 只会抛「read project.json: 找不到文件」，用户不知道
// 是选错目录还是工程损坏；先探测一次就能给出准确提示（是工程 → 直接打开，
// 是空目录 → 建议新建）。
func (p *ProjectService) InspectProjectDir(dir string) map[string]any {
	out := map[string]any{"path": dir, "exists": false, "is_project": false, "name": ""}
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		return out
	}
	out["exists"] = true
	if meta, err := project.LoadMeta(dir); err == nil {
		out["is_project"] = true
		out["name"] = meta.Name
	}
	return out
}

// ListRecentProjects 返回最近工程（已标记失效条目）。
func (p *ProjectService) ListRecentProjects() []map[string]any {
	p.recents.Validate()
	list := p.recents.List()
	out := make([]map[string]any, 0, len(list))
	for _, e := range list {
		out = append(out, map[string]any{
			"path": e.Path, "name": e.Name,
			"last_opened": e.LastOpened.Format("2006-01-02T15:04:05Z07:00"),
			"pinned":      e.Pinned, "missing": e.Missing,
		})
	}
	return out
}

func (p *ProjectService) RemoveRecentProject(path string) error {
	if err := p.recents.Remove(path); err != nil {
		return err
	}
	p.emitRecents()
	return nil
}

func (p *ProjectService) emitRecents() {
	if e := p.emitter(); e != nil {
		e(events.EvRecentUpdated, map[string]any{"projects": p.ListRecentProjects()})
	}
}

func (p *ProjectService) emitter() events.Emitter {
	app := application.Get()
	if app == nil {
		return nil
	}
	return func(event string, payload map[string]any) {
		app.Event.Emit(event, payload)
	}
}
