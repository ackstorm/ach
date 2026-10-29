// ChartFrame.tsx — the one chart style. A desktop/mobile pair of fixed-ratio
// SVGs (swapped by CSS, never stretched, so axis text keeps its shape), with
// gridlines and Y labels at 0/50/100% and the first/last X labels. A chart
// draws only its marks, from the frame's x (fraction 0..1) and y (value) scales.
// With `hover`, the point nearest the pointer gets a guide line, dots and a
// card listing that point's figures.
import { useState, type MouseEvent, type ReactNode } from 'react';

export interface Frame { left: number; right: number; bottom: number; x: (fraction: number) => number; y: (value: number) => number }

// One hovered point: a title and its rows; a row with `y` also gets a dot at that value.
export interface Tip { title: string; rows: { label: string; value: string; color: string; y?: number }[] }

// n points, point i at x fraction at(i); tip(i) describes it from figures the chart already draws.
export interface Hover { n: number; at: (i: number) => number; tip: (i: number) => Tip }

const TICKS = [0, 0.5, 1];
const HEIGHT = 190;

// `wide` is the desktop viewBox width: match the panel's usual width so the
// labels land at the same rendered size on every chart.
export function ChartFrame({ max, format, xLabels, wide = 820, hover, children }: { max: number; format: (value: number) => string; xLabels: [string, string]; wide?: number; hover?: Hover; children: (frame: Frame) => ReactNode }) {
  const [on, setOn] = useState<{ i: number; left: number; top: number; flip: boolean } | null>(null);
  const tip = on && hover && on.i < hover.n ? hover.tip(on.i) : null;
  return <>{[wide, 340].map((width) => {
    const left = width === wide ? 48 : 52;
    const right = width - 12;
    const frame: Frame = { left, right, bottom: 150, x: (f) => left + f * (right - left), y: (v) => 150 - v / max * 125 };
    // The SVG keeps its ratio (centred), so the pointer maps through the rendered scale and offset.
    const move = (e: MouseEvent<SVGSVGElement>) => {
      if (!hover?.n) return;
      const r = e.currentTarget.getBoundingClientRect();
      const scale = Math.min(r.width / width, r.height / HEIGHT);
      if (!scale) return;
      const vx = (e.clientX - r.left - (r.width - width * scale) / 2) / scale;
      let i = 0;
      for (let j = 1; j < hover.n; j++) if (Math.abs(frame.x(hover.at(j)) - vx) < Math.abs(frame.x(hover.at(i)) - vx)) i = j;
      const box = (e.currentTarget.parentElement ?? e.currentTarget).getBoundingClientRect();
      setOn({ i, left: e.clientX - box.left, top: e.clientY - box.top, flip: e.clientX - box.left > box.width / 2 });
    };
    return <svg key={width} className={width === wide ? 'desktop-chart' : 'mobile-chart'} viewBox={`0 0 ${width} ${HEIGHT}`} aria-hidden="true" onMouseMove={move} onMouseLeave={() => setOn(null)}>
      {TICKS.map((f) => <g key={f}><line className="grid-line" x1={left} x2={right} y1={frame.y(f * max)} y2={frame.y(f * max)} /><text className="chart-label" x="0" y={frame.y(f * max) + 4}>{format(f * max)}</text></g>)}
      {children(frame)}
      {tip && <g className="chart-hover"><line className="chart-guide" x1={frame.x(hover!.at(on!.i))} x2={frame.x(hover!.at(on!.i))} y1={frame.y(max)} y2={frame.bottom} />{tip.rows.map((row) => row.y != null && <circle key={row.label} cx={frame.x(hover!.at(on!.i))} cy={frame.y(row.y)} r="4" fill={row.color} />)}</g>}
      <text className="chart-label" x={left} y="180">{xLabels[0]}</text><text className="chart-label" x={right} y="180" textAnchor="end">{xLabels[1]}</text>
    </svg>;
  })}
  {tip && <div className={`chart-tip${on!.flip ? ' flip' : ''}`} style={{ left: on!.left, top: on!.top }} aria-hidden="true"><b>{tip.title}</b>{tip.rows.map((row) => <span key={row.label}><i style={{ background: row.color }} />{row.label}<em>{row.value}</em></span>)}</div>}
  </>;
}
