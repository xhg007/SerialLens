import { Dialogs } from "@wailsio/runtime";

// 系统原生目录选择器（Wails v3 runtime dialogs，走各平台原生对话框）。
//
// 注意返回值：Go 侧单选用 PromptForSingleSelection 返回 string，
// 多选才返回 []string；runtime 的 TS 类型统一标成 string[]，所以这里两种都接。
export async function pickDirectory(opts: { title: string; buttonText?: string; defaultDir?: string }): Promise<string> {
  try {
    const res = await Dialogs.OpenFile({
      CanChooseDirectories: true,
      CanChooseFiles: false,
      CanCreateDirectories: true,
      AllowsMultipleSelection: false,
      ShowHiddenFiles: false,
      Title: opts.title,
      ButtonText: opts.buttonText ?? "选择此目录",
      ...(opts.defaultDir ? { Directory: opts.defaultDir } : {}),
    } as any);

    const picked = Array.isArray(res) ? res[0] : (res as unknown as string);
    return (picked ?? "").trim();
  } catch (e) {
    // 取消在部分平台会走 reject，不该当成错误弹红条
    const msg = e instanceof Error ? e.message : String(e ?? "");
    if (/cancel|取消/i.test(msg)) return "";
    throw e;
  }
}

/** 取路径最后一段，用作工程名默认值（兼容 Windows 反斜杠与末尾斜杠）。 */
export function baseName(path: string): string {
  const parts = path.split(/[\\/]+/).filter(Boolean);
  return parts[parts.length - 1] ?? "";
}

// 系统原生保存对话框，返回所选路径（取消返回空串）。
export async function saveFileDialog(opts: {
  title: string;
  defaultName: string;
  filters?: { name: string; pattern: string }[];
}): Promise<string> {
  try {
    const res = await Dialogs.SaveFile({
      Title: opts.title,
      Filename: opts.defaultName,
      CanChooseFiles: true,
      CanCreateDirectories: true,
      ButtonText: "保存",
      ...(opts.filters ? { Filters: opts.filters } : {}),
    } as any);
    return (res ?? "").trim();
  } catch (e) {
    const msg = e instanceof Error ? e.message : String(e ?? "");
    if (/cancel|取消/i.test(msg)) return "";
    throw e;
  }
}
