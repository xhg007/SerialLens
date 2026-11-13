// 通道数据缓冲：所有通道共享一条时间轴（33ms 合批 → 同一批共享 ts），
// 缺数据的通道填 NaN，uPlot 会自然断线。
//
// 用模块级单例而不是放进 React state：100Hz × 10 通道时每秒 30 批、300 点，
// 走 state 会让整棵树每秒重渲染 30 次；这里只在批次提交时 bump 一个版本号。

const MAX_ROWS = 20000; // 100Hz 下约 200 秒历史
const TRIM_ROWS = 2000; // 溢出时一次裁掉一批，避免每点都 splice

export interface ChartPoint {
  ch: string;
  v: number;
  ts: number;
}

export class SeriesBuffer {
  xs: number[] = [];
  cols = new Map<string, number[]>();
  order: string[] = [];

  /** 清空（工程切换/图表重载）。 */
  reset() {
    this.xs = [];
    this.cols.clear();
    this.order = [];
  }

  /** 推送一批点（同一批可能含多个通道，ts 相同或递增）。 */
  push(points: ChartPoint[]) {
    for (const p of points) {
      if (!Number.isFinite(p.v)) continue;
      const col = this.ensureCol(p.ch);
      const lastIdx = this.xs.length - 1;
      if (lastIdx >= 0 && this.xs[lastIdx] === p.ts) {
        col[lastIdx] = p.v;
        continue;
      }
      if (lastIdx >= 0 && p.ts < this.xs[lastIdx]) {
        // 时间倒退：写回同一行，避免时间轴乱序把 uPlot 搞崩
        col[lastIdx] = p.v;
        continue;
      }
      this.xs.push(p.ts);
      for (const c of this.cols.values()) c.push(NaN);
      col[this.xs.length - 1] = p.v;
    }
    this.trim();
  }

  /** 最近 windowSec 秒的数据，转成 uPlot 需要的 [xs(秒), ...ys]。 */
  build(channels: string[], windowSec: number): { xs: number[]; ys: number[][] } {
    const n = this.xs.length;
    if (n === 0) return { xs: [], ys: channels.map(() => []) };
    const cutoff = this.xs[n - 1] - windowSec * 1000;
    let start = 0;
    // 线性扫描尾部即可：窗口外的行只在头部
    while (start < n && this.xs[start] < cutoff) start++;
    const xs = new Array<number>(n - start);
    for (let i = start; i < n; i++) xs[i - start] = this.xs[i] / 1000;
    const ys = channels.map((ch) => {
      const col = this.cols.get(ch);
      if (!col) return [] as number[];
      const out = new Array<number>(n - start);
      for (let i = start; i < n; i++) {
        const v = col[i];
        out[i - start] = Number.isNaN(v) ? NaN : v;
      }
      return out;
    });
    return { xs, ys };
  }

  /** 某通道最新值。 */
  latest(ch: string): number | null {
    const col = this.cols.get(ch);
    if (!col) return null;
    for (let i = col.length - 1; i >= 0; i--) {
      if (!Number.isNaN(col[i])) return col[i];
    }
    return null;
  }

  /** 已知通道（按首次出现顺序）。 */
  knownChannels(): string[] {
    return this.order.slice();
  }

  private ensureCol(ch: string): number[] {
    let col = this.cols.get(ch);
    if (!col) {
      col = new Array<number>(this.xs.length).fill(NaN);
      this.cols.set(ch, col);
      this.order.push(ch);
    }
    return col;
  }

  private trim() {
    if (this.xs.length <= MAX_ROWS) return;
    const drop = this.xs.length - MAX_ROWS + TRIM_ROWS;
    this.xs.splice(0, drop);
    for (const c of this.cols.values()) c.splice(0, drop);
  }
}

export const seriesBuffer = new SeriesBuffer();
