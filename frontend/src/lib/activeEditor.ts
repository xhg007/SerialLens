// Monaco 编辑器实例的最小适配层：右键菜单只需要这三个动作，
// 拿到独立模块是为了不让主包 import monaco（编辑器是 3.3MB 的懒加载 chunk）。
export interface ContextEditor {
  getSelectedText(): string;
  paste(text: string): void;
  selectAll(): void;
}

let current: ContextEditor | null = null;

export function setContextEditor(editor: ContextEditor | null) {
  current = editor;
}

export function getContextEditor(): ContextEditor | null {
  return current;
}
