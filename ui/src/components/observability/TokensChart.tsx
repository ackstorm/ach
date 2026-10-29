import type { StatsSeriesPoint } from '@/lib/api-types';
import { abbreviate, formatShortDay } from '@/lib/format';
import { ChartFrame } from '@/components/charts/ChartFrame';

/** Daily input and output tokens from the returned series, one line each on a shared scale. */
export function TokensChart({ series }: { series: StatsSeriesPoint[] }) {
  if (!series.length) return <p className="report-notice">No activity in this period.</p>;
  const ceiling = Math.max(1, ...series.map((point) => Math.max(point.input_tokens, point.output_tokens)));
  const totalIn = series.reduce((sum, point) => sum + point.input_tokens, 0);
  const totalOut = series.reduce((sum, point) => sum + point.output_tokens, 0);
  return <div className="runway-chart" role="img" aria-label={`Tokens per day: ${abbreviate(totalIn)} input, ${abbreviate(totalOut)} output`}>
    <ChartFrame max={ceiling} format={abbreviate} xLabels={[formatShortDay(series[0].date), formatShortDay(series.at(-1)?.date)]} hover={{ n: series.length, at: (i) => i / Math.max(1, series.length - 1), tip: (i) => ({ title: formatShortDay(series[i].date), rows: [
      { label: 'Input', value: abbreviate(series[i].input_tokens), color: 'var(--green)', y: series[i].input_tokens },
      { label: 'Output', value: abbreviate(series[i].output_tokens), color: 'var(--blue)', y: series[i].output_tokens },
    ] }) }}>{(f) => {
      const path = (pick: (point: StatsSeriesPoint) => number) => series.map((point, index) => `${index ? 'L' : 'M'}${f.x(index / Math.max(1, series.length - 1))} ${f.y(pick(point))}`).join(' ');
      return <>
        <path className="tokens-line tokens-in" d={path((point) => point.input_tokens)} />
        <path className="tokens-line tokens-out" d={path((point) => point.output_tokens)} />
        {series.length === 1 && [[series[0].input_tokens, 'var(--green)'], [series[0].output_tokens, 'var(--blue)']].map(([v, fill]) => <circle key={String(fill)} cx={f.left} cy={f.y(Number(v))} r="4" fill={String(fill)} />)}
      </>;
    }}</ChartFrame>
  </div>;
}
