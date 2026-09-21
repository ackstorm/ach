// ExportCsvButton.tsx — caption-styled button that serializes the given rows to
// CSV (lib/csv) and triggers a client-side download. Renders nothing for an
// empty row set (nothing to export).

import * as React from 'react';

import { downloadCsv, toCsv, type CsvColumn } from '@/lib/csv';

export function ExportCsvButton<T>({
  rows,
  columns,
  filename,
}: {
  rows: T[];
  columns: CsvColumn<T>[];
  filename: string;
}): React.ReactElement | null {
  if (rows.length === 0) return null;
  return (
    <button
      type="button"
      data-slot="export-csv"
      title="Export CSV"
      aria-label="Export CSV"
      onClick={() => downloadCsv(filename, toCsv(rows, columns))}
      className="inline-flex cursor-pointer items-center justify-center rounded-md border border-border p-1.5 text-text-secondary transition-colors hover:border-text-secondary hover:text-text-primary"
    >
      <svg
        width="14"
        height="14"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden="true"
      >
        <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
        <polyline points="7 10 12 15 17 10" />
        <line x1="12" x2="12" y1="15" y2="3" />
      </svg>
    </button>
  );
}
