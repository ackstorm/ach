import * as React from 'react';

import { sortRows, type SortDir } from '@/lib/sort';
import { cn } from '@/lib/utils';
import { formatInt } from '@/lib/format';

export type DataTableColumn<T> = {
  /** Unique column id (React key + data-col). */
  key: string;
  /** Header cell content. */
  header: React.ReactNode;
  /** Data cell renderer. */
  cell: (row: T) => React.ReactNode;
  /** Optional <td> className. */
  className?: string;
  /** Optional <th> className. */
  headerClassName?: string;
  /**
   * When set, this column's header becomes an interactive sort toggle keyed on
   * the value this accessor returns (null/undefined sort last). Columns without
   * an accessor stay plain, non-clickable headers — sort is fully opt-in, so
   * existing tables are unchanged until they declare accessors.
   */
  sortAccessor?: (row: T) => number | string | null | undefined;
};

export type DataTableSort = { key: string; dir: SortDir };

export type DataTableProps<T> = {
  columns: DataTableColumn<T>[];
  rows: T[];
  /** Stable React key per row. */
  getRowId: (row: T) => string;
  /** Shown (spanning all columns) when rows is empty. */
  empty?: React.ReactNode;
  /** Optional trailing right-aligned actions cell. */
  rowActions?: (row: T) => React.ReactNode;
  /** Optional per-row <tr> className (e.g. dim an inactive row). */
  rowClassName?: (row: T) => string | undefined;
  /** Row id (getRowId) to mark `.on`, e.g. the slice hovered in a linked chart. */
  highlight?: string | null;
  /** Optional row click (the row is not focusable; give it a focusable cell too). */
  onRowClick?: (row: T) => void;
  /**
   * Header label for the trailing actions column. When omitted the header is
   * visually hidden (sr-only "Actions"); pass a node to show a real label (e.g.
   * "Action"). The actions column always hugs its content width.
   */
  actionsHeader?: React.ReactNode;
  /** Optional footer/caption region rendered below the table. */
  caption?: React.ReactNode;
  /**
   * Initial sort (must reference a column whose `sortAccessor` is set). When
   * omitted, rows render in the order given until the user clicks a sortable
   * header. Clicking the active column toggles direction; a new column starts
   * descending.
   */
  defaultSort?: DataTableSort;
  /**
   * Server-side sort: the caller owns the order. `sort` is the active column
   * and `onSort` gets the next one; rows render as given. A column is sortable
   * when it has a `sortAccessor` (its value is then unused).
   */
  sort?: DataTableSort;
  onSort?: (sort: DataTableSort) => void;
  /** Rows kept above the sorted rows on every page (e.g. an "All spend" total). */
  pinned?: T[];
  /** Rows kept below the sorted rows, unsorted (e.g. a muted "Trivial" bucket). */
  pinnedEnd?: T[];
  /** Client-side pages of this size, with the standard footer pager. */
  pageSize?: number;
  /** Left side of the footer row (a note); the pager sits right. */
  footer?: React.ReactNode;
  /** Server pagination: replaces the client pager in the footer. */
  pager?: React.ReactNode;
  /** Wrapper className. */
  className?: string;
} & Omit<React.ComponentProps<'div'>, 'children'>;

// A tiny CSS-triangle sort indicator (no text glyph, so getByText on the header
// label still matches). Points down for desc, up for asc; dimmed when the column
// is sortable but not the active sort key.
export function SortIndicator({
  active,
  dir,
}: {
  active: boolean;
  dir: SortDir;
}): React.ReactElement {
  const up = active && dir === 'asc';
  return (
    <span
      aria-hidden="true"
      className={cn(
        'inline-block size-0 border-x-[3px] border-x-transparent transition-opacity',
        up
          ? 'border-b-[4px] border-b-current'
          : 'border-t-[4px] border-t-current',
        active ? 'opacity-100' : 'opacity-30'
      )}
    />
  );
}

/** The one table pager: "Previous · 1–10 of N · Next". */
export function Pager({ start, count, total, onPrev, onNext }: { start: number; count: number; total: number; onPrev?: () => void; onNext?: () => void }): React.ReactElement {
  return (
    <div className="report-pager">
      <button type="button" className="btn" disabled={!onPrev} onClick={onPrev}>Previous</button>
      <span>{formatInt(start + 1)}–{formatInt(start + count)} of {formatInt(total)}</span>
      <button type="button" className="btn" disabled={!onNext} onClick={onNext}>Next</button>
    </div>
  );
}

// DataTable — the ONE table: Keys, Models, Cost detail, Performance, Traces,
// Work classification, Workloads. Styles: `.table` in styles/ledger-ui.css.
// Sortable headers carry aria-sort and a triangle; the footer row is a note on
// the left and the pager on the right.
export function DataTable<T>({
  columns,
  rows,
  getRowId,
  empty,
  rowActions,
  rowClassName,
  highlight,
  onRowClick,
  actionsHeader,
  caption,
  defaultSort,
  sort: controlled,
  onSort,
  pinned = [],
  pinnedEnd = [],
  pageSize,
  footer,
  pager,
  className,
  ...rest
}: DataTableProps<T>): React.ReactElement {
  const hasActions = rowActions !== undefined;
  const colSpan = columns.length + (hasActions ? 1 : 0);

  const [own, setOwn] = React.useState<DataTableSort | null>(defaultSort ?? null);
  const sort = controlled ?? own;
  const [page, setPage] = React.useState(0);

  const toggleSort = (key: string) => {
    const next: DataTableSort = sort && sort.key === key ? { key, dir: sort.dir === 'asc' ? 'desc' : 'asc' } : { key, dir: 'desc' };
    if (onSort) onSort(next);
    else setOwn(next);
    setPage(0);
  };

  const sorted = React.useMemo(() => {
    if (!sort || onSort) return rows;
    const col = columns.find((c) => c.key === sort.key);
    if (!col?.sortAccessor) return rows;
    return sortRows(rows, col.sortAccessor, sort.dir);
  }, [rows, columns, sort, onSort]);

  const last = pageSize ? Math.max(0, Math.ceil(sorted.length / pageSize) - 1) : 0;
  const current = Math.min(page, last);
  const display = pageSize ? sorted.slice(current * pageSize, current * pageSize + pageSize) : sorted;
  const clientPager = pageSize && sorted.length > pageSize
    ? <Pager start={current * pageSize} count={display.length} total={sorted.length} onPrev={current > 0 ? () => setPage(current - 1) : undefined} onNext={current < last ? () => setPage(current + 1) : undefined} />
    : null;
  const pagerNode = pager ?? clientPager;
  const shown = [...pinned, ...display, ...(current === last ? pinnedEnd : [])];

  return (
    <div data-slot="data-table" className={cn('table', className)} {...rest}>
      <div className="table-scroll">
        <table data-slot="data-table-table">
          <thead data-slot="data-table-header">
            <tr>
              {columns.map((column) => {
                const sortable = column.sortAccessor !== undefined;
                const isActive = sort?.key === column.key;
                return (
                  <th
                    key={column.key}
                    scope="col"
                    data-col={column.key}
                    aria-sort={sortable ? (isActive ? (sort?.dir === 'asc' ? 'ascending' : 'descending') : 'none') : undefined}
                    className={column.headerClassName}
                  >
                    {sortable ? (
                      <button type="button" data-slot="data-table-sort" onClick={() => toggleSort(column.key)} className={cn('th-sort', isActive && 'on')}>
                        {column.header}
                        <SortIndicator active={isActive} dir={isActive && sort ? sort.dir : 'desc'} />
                      </button>
                    ) : (
                      column.header
                    )}
                  </th>
                );
              })}
              {hasActions ? (
                <th scope="col" data-col="__actions" className="th-actions">
                  {actionsHeader ?? <span className="sr-only">Actions</span>}
                </th>
              ) : null}
            </tr>
          </thead>
          <tbody data-slot="data-table-body">
            {shown.length === 0 ? (
              <tr data-slot="data-table-empty">
                <td colSpan={colSpan} className="td-empty">{empty ?? 'No items'}</td>
              </tr>
            ) : (
              shown.map((row, i) => (
                <tr
                  key={getRowId(row)}
                  data-slot="data-table-row"
                  data-row-id={getRowId(row)}
                  className={cn(i < pinned.length && 'pinned', onRowClick && 'clickable', highlight != null && getRowId(row) === highlight && 'on', rowClassName?.(row))}
                  onClick={onRowClick ? () => onRowClick(row) : undefined}
                >
                  {columns.map((column) => (
                    <td key={column.key} data-col={column.key} className={column.className}>
                      {column.cell(row)}
                    </td>
                  ))}
                  {hasActions ? <td data-col="__actions" className="td-actions">{rowActions(row)}</td> : null}
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
      {footer || pagerNode ? <div className="report-foot">{footer}{pagerNode}</div> : null}
      {caption !== undefined ? <div data-slot="data-table-caption" className="table-caption">{caption}</div> : null}
    </div>
  );
}
