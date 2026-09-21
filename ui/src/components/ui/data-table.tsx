import * as React from 'react';

import { sortRows, type SortDir } from '@/lib/sort';
import { cn } from '@/lib/utils';

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

export function DataTable<T>({
  columns,
  rows,
  getRowId,
  empty,
  rowActions,
  rowClassName,
  actionsHeader,
  caption,
  defaultSort,
  className,
  ...rest
}: DataTableProps<T>): React.ReactElement {
  const hasActions = rowActions !== undefined;
  const colSpan = columns.length + (hasActions ? 1 : 0);

  const [sort, setSort] = React.useState<DataTableSort | null>(
    defaultSort ?? null
  );

  const toggleSort = (key: string) =>
    setSort((cur) =>
      cur && cur.key === key
        ? { key, dir: cur.dir === 'asc' ? 'desc' : 'asc' }
        : { key, dir: 'desc' }
    );

  const display = React.useMemo(() => {
    if (!sort) return rows;
    const col = columns.find((c) => c.key === sort.key);
    if (!col?.sortAccessor) return rows;
    return sortRows(rows, col.sortAccessor, sort.dir);
  }, [rows, columns, sort]);

  return (
    <div
      data-slot="data-table"
      className={cn(
        'bg-card text-card-foreground overflow-x-auto rounded-xl border',
        className
      )}
      {...rest}
    >
      <table data-slot="data-table-table" className="w-full border-collapse">
        <thead data-slot="data-table-header">
          <tr className="border-b">
            {columns.map((column) => {
              const sortable = column.sortAccessor !== undefined;
              const isActive = sort?.key === column.key;
              return (
                <th
                  key={column.key}
                  scope="col"
                  data-col={column.key}
                  aria-sort={
                    sortable
                      ? isActive
                        ? sort?.dir === 'asc'
                          ? 'ascending'
                          : 'descending'
                        : 'none'
                      : undefined
                  }
                  className={cn(
                    'text-muted-foreground px-4 py-3 text-left text-xs font-semibold tracking-wider uppercase',
                    column.headerClassName
                  )}
                >
                  {sortable ? (
                    <button
                      type="button"
                      data-slot="data-table-sort"
                      onClick={() => toggleSort(column.key)}
                      className={cn(
                        'inline-flex cursor-pointer items-center gap-1 uppercase transition-colors hover:text-text-primary',
                        isActive && 'text-text-primary'
                      )}
                    >
                      {column.header}
                      <SortIndicator
                        active={isActive}
                        dir={isActive && sort ? sort.dir : 'desc'}
                      />
                    </button>
                  ) : (
                    column.header
                  )}
                </th>
              );
            })}
            {hasActions ? (
              <th
                scope="col"
                data-col="__actions"
                className="text-muted-foreground w-px px-4 py-3 text-right text-xs font-semibold tracking-wider whitespace-nowrap uppercase"
              >
                {actionsHeader ?? <span className="sr-only">Actions</span>}
              </th>
            ) : null}
          </tr>
        </thead>
        <tbody data-slot="data-table-body">
          {display.length === 0 ? (
            <tr data-slot="data-table-empty">
              <td colSpan={colSpan} className="text-muted-foreground px-4 py-6 text-center text-sm">
                {empty ?? 'No items'}
              </td>
            </tr>
          ) : (
            display.map((row, index) => (
              <tr
                key={getRowId(row)}
                data-slot="data-table-row"
                className={cn(index < display.length - 1 && 'border-b', rowClassName?.(row))}
              >
                {columns.map((column) => (
                  <td
                    key={column.key}
                    data-col={column.key}
                    className={cn('px-4 py-3 text-sm', column.className)}
                  >
                    {column.cell(row)}
                  </td>
                ))}
                {hasActions ? (
                  <td
                    data-col="__actions"
                    className="w-px px-4 py-3 text-right text-sm whitespace-nowrap"
                  >
                    {rowActions(row)}
                  </td>
                ) : null}
              </tr>
            ))
          )}
        </tbody>
      </table>
      {caption !== undefined ? (
        <div data-slot="data-table-caption" className="text-muted-foreground border-t px-4 py-3 text-sm">
          {caption}
        </div>
      ) : null}
    </div>
  );
}
