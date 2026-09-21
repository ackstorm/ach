// csv.ts — minimal CSV serializer + browser download for the stats exports.
//
// PURE serializer (node-env unit-testable) + a tiny DOM download helper. Cells
// are RFC-4180 quoted; a cell starting with = + - @ is prefixed with ' to defuse
// spreadsheet formula injection (model names and key aliases are
// user-influenced strings).

export interface CsvColumn<T> {
  key: keyof T & string;
  header: string;
}

const FORMULA_PREFIX = /^[=+\-@]/;

function cell(value: unknown): string {
  if (value === null || value === undefined) return '';
  let s = String(value);
  if (FORMULA_PREFIX.test(s)) s = `'${s}`;
  if (/[",\n]/.test(s)) s = `"${s.replace(/"/g, '""')}"`;
  return s;
}

export function toCsv<T>(rows: T[], columns: CsvColumn<T>[]): string {
  const head = columns.map((c) => cell(c.header)).join(',');
  const body = rows.map((r) =>
    columns.map((c) => cell((r as Record<string, unknown>)[c.key])).join(',')
  );
  return [head, ...body].join('\n') + '\n';
}

// Trigger a client-side download of the CSV (no fetch — exports page state).
export function downloadCsv(filename: string, csv: string): void {
  const blob = new Blob([csv], { type: 'text/csv;charset=utf-8' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  a.click();
  URL.revokeObjectURL(url);
}
