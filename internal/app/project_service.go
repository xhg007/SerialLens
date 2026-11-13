package app

import (
	"context"

	"github.com/wailsapp/wails/v3/pkg/application"

	"seriallens/internal/events"
	"seriallens/internal/project"
)

// ProjectService Wails 薄壳：工程域方法绑定 + 事件桥接。
// 业务逻辑全部在 internal/project。
type ProjectService struct {
	svc     *project.Service
	recents *project.RecentStore
}

func NewProjectService() *ProjectService {
	recents := project.NewRecentStore("")
	return &ProjectService{
		recents: recents,
		svc:     project.NewService(recents, nil),
	}
}

// ServiceStartup 在应用启动时注入事件桥接。
func (p *ProjectService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	if app := application.Get(); app != nil {
		p.svc.SetEmitter(func(event string, payload map[string]any) {
			app.Event.Emit(event, payload)
		})
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
