// KeysTable.tsx — the keys-as-DATA-TABLE surface, rewired onto ACH's
// GET /platform/keys?type=ek (render.KeyListRow) — replaces the alitellm-auth
// /api/session/{keys,teams} surface.
//
// This component OWNS its data: it calls useKeys() internally. The DELETE
// (revoke) confirm modal is wired by the parent (Dashboard); KeysTable only
// invokes onDelete(row) from the per-row kebab.
//
// SECURITY INVARIANTS (threat register 10-03 / 14-03):
//   • The table NEVER renders a secret. The full plaintext of a freshly-created
//     key is shown exactly once, in the create-key modal at mint time ("you
//     won't see it again"); from then on the table shows only the PUBLIC
//     key_id, masked to a short prefix. There is no reveal and no copy action
//     here.

import * as React from 'react';
import { MoreVertical, Trash2 } from 'lucide-react';

import { Badge } from '@/components/ui/badge';
import {
  DataTable,
  type DataTableColumn,
} from '@/components/ui/data-table';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip';
import { useKeys, useResumeKey, useSuspendKey } from '@/hooks/use-keys';
import { useToast } from '@/hooks/use-toast';
import { formatDate } from '@/lib/format';
import { selectKeyRows } from '@/lib/keys';
import { isStale, relativeTime } from '@/lib/relative-time';
import { cn } from '@/lib/utils';
import { useSessionStore } from '@/stores/session';
import type { KeyRow } from '@/lib/api-types';

// The em-dash placeholder (matches format.ts EM_DASH) for an empty cell.
const EM_DASH = '—';

// How many leading characters of the public key id to show in the table.
const KEY_ID_MAX = 16;

// Display the public key id as up to KEY_ID_MAX leading chars, with a trailing
// ellipsis when it is longer. The full id is intentionally not surfaced.
function shortKeyId(id: string): string {
  return id.length > KEY_ID_MAX ? `${id.slice(0, KEY_ID_MAX)}…` : id;
}

type KeysTableProps = {
  /** Invoked by the per-row kebab's Revoke action. The parent owns the confirm + DELETE. */
  onDelete: (row: KeyRow) => void;
};

/** Display label + Badge variant for each of the five effective states (D-30). */
const STATE_META: Record<
  KeyRow['status'],
  { label: string; variant: 'default' | 'outline' | 'destructive' | 'secondary' }
> = {
  active: { label: 'Active', variant: 'default' },
  suspended: { label: 'Suspended', variant: 'secondary' },
  expired: { label: 'Expired', variant: 'outline' },
  invalid: { label: 'Invalid', variant: 'outline' },
  revoked: { label: 'Revoked', variant: 'destructive' },
};

// Sort precedence for the State column (ascending = Active first, Revoked last).
const STATE_RANK: Record<KeyRow['status'], number> = {
  active: 0,
  suspended: 1,
  expired: 2,
  invalid: 3,
  revoked: 4,
};

export function KeysTable({ onDelete }: KeysTableProps): React.ReactElement {
  const query = useKeys();
  const suspendKey = useSuspendKey();
  const resumeKey = useResumeKey();
  const { toast } = useToast();
  const suspendPropagationSeconds = useSessionStore(
    (s) => s.me?.suspend_propagation_seconds,
  );
  const rows = selectKeyRows(query.data);

  const onSuspend = (row: KeyRow) => {
    suspendKey.mutate(row.key_id, {
      onSuccess: () => {
        toast({
          message: `Suspension may take up to ${suspendPropagationSeconds ?? 60} seconds to apply. Requests already in progress may continue.`,
          variant: 'info',
        });
      },
    });
  };
  const onResume = (row: KeyRow) => resumeKey.mutate(row.key_id);

  // ── State branches ───────────────────────────────────────────────────────
  if (query.isPending) {
    return (
      <div
        data-slot="keys-table-state"
        className="bg-card text-card-foreground rounded-xl border px-4 py-12 text-center"
      >
        <p className="text-muted-foreground text-sm">Loading your keys…</p>
      </div>
    );
  }

  if (query.isError) {
    return (
      <div
        data-slot="keys-table-state"
        className="bg-card text-card-foreground rounded-xl border px-4 py-12 text-center"
      >
        <p className="text-destructive text-sm">
          Couldn&apos;t load your keys. Refresh the page to try again.
        </p>
      </div>
    );
  }

  const columns: DataTableColumn<KeyRow>[] = [
    {
      key: 'name',
      header: 'Name',
      headerClassName: 'whitespace-nowrap',
      className: 'align-top max-w-[220px]',
      cell: (row) => {
        const short = shortKeyId(row.key_id);
        const name = row.name || short;
        const showChip = Boolean(row.name);
        return (
          <div className="flex min-w-0 flex-col gap-0.5">
            <span className="text-foreground truncate text-sm font-semibold">
              {name}
            </span>
            {showChip ? (
              <span
                data-slot="key-chip"
                className="text-muted-foreground font-mono text-xs break-all"
              >
                id:{short}
              </span>
            ) : null}
          </div>
        );
      },
      sortAccessor: (row) => row.name || row.key_id,
    },
    {
      key: 'environment',
      header: 'Environment',
      className: 'font-mono text-xs whitespace-nowrap',
      cell: (row) => row.environment || EM_DASH,
      sortAccessor: (row) => row.environment,
    },
    {
      key: 'state',
      header: 'State',
      cell: (row) => {
        const { label, variant } = STATE_META[row.status];
        const badge = <Badge variant={variant}>{label}</Badge>;
        if (!row.reasons?.length) return badge;
        return (
          <Tooltip>
            <TooltipTrigger asChild>{badge}</TooltipTrigger>
            <TooltipContent>{row.reasons.join(', ')}</TooltipContent>
          </Tooltip>
        );
      },
      sortAccessor: (row) => STATE_RANK[row.status],
    },
    {
      key: 'created',
      header: 'Created',
      className: 'font-mono text-xs whitespace-nowrap',
      cell: (row) => formatDate(row.created_at),
      sortAccessor: (row) => row.created_at,
    },
    {
      key: 'lastused',
      header: 'Last used',
      className: 'font-mono text-xs whitespace-nowrap',
      cell: (row) => {
        const lastUsed = row.last_used_at ?? null;
        const stale = isStale(lastUsed);
        return (
          <span
            data-slot="key-lastused"
            data-testid={stale ? 'key-lastused-stale' : 'key-lastused'}
            className={stale ? 'text-amber-600 dark:text-amber-400' : undefined}
            title={lastUsed ?? undefined}
          >
            {relativeTime(lastUsed)}
          </span>
        );
      },
      // Sort by the raw timestamp (null sorts last); null last_used_at = never used.
      sortAccessor: (row) => row.last_used_at ?? null,
    },
    {
      key: 'expires',
      header: 'Expires',
      className: 'font-mono text-xs whitespace-nowrap',
      cell: (row) => (row.expires_at == null ? 'Never' : formatDate(row.expires_at)),
      sortAccessor: (row) => row.expires_at,
    },
  ];

  return (
    <TooltipProvider>
      <div className="flex flex-col gap-3">
        <DataTable
          data-slot="keys-table"
          columns={columns}
          rows={rows}
          // Default sort so the active-sort marker shows on load (newest keys first);
          // every other column header stays click-to-sort.
          defaultSort={{ key: 'created', dir: 'desc' }}
          getRowId={(row) => row.key_id}
          rowClassName={(row) => cn(row.status === 'revoked' && 'opacity-40')}
          actionsHeader="Action"
          empty={
            <div data-slot="keys-table-empty" className="py-6">
              <p className="text-foreground text-base font-semibold">No API Keys</p>
              <p className="text-muted-foreground mt-1 text-sm">
                You have no virtual keys yet. Create one to get started.
              </p>
            </div>
          }
          rowActions={(row) => {
            const canSuspend = row.status === 'active' || row.status === 'invalid';
            const canResume = row.status === 'suspended';
            const canRevoke = row.status !== 'revoked';
            return (
              <div className="flex justify-end">
                {/* Single per-row control — a kebab menu. Revoke lives INSIDE it (as a
                    destructive item) rather than as an always-visible red trash on
                    every row, so the table doesn't read as "delete everything". */}
                <DropdownMenu>
                  <DropdownMenuTrigger
                    data-slot="key-menu"
                    aria-label="More actions"
                    className="text-muted-foreground border-border hover:border-primary hover:text-primary inline-flex size-7 cursor-pointer items-center justify-center rounded-md border transition-colors outline-none focus-visible:border-primary"
                  >
                    <MoreVertical className="size-[15px]" aria-hidden="true" />
                  </DropdownMenuTrigger>
                  <DropdownMenuContent>
                    {!canSuspend && !canResume && !canRevoke ? (
                      <DropdownMenuItem disabled data-slot="key-no-actions">
                        No actions available
                      </DropdownMenuItem>
                    ) : null}
                    {canSuspend ? (
                      <DropdownMenuItem
                        data-slot="key-suspend"
                        onSelect={() => onSuspend(row)}
                      >
                        Suspend key
                      </DropdownMenuItem>
                    ) : null}
                    {canResume ? (
                      <DropdownMenuItem
                        data-slot="key-resume"
                        onSelect={() => onResume(row)}
                      >
                        Resume key
                      </DropdownMenuItem>
                    ) : null}
                    {canRevoke ? (
                      <>
                        {canSuspend || canResume ? <DropdownMenuSeparator /> : null}
                        <DropdownMenuItem
                          data-slot="key-delete"
                          onSelect={() => onDelete(row)}
                          className="text-destructive focus:bg-destructive/10 focus:text-destructive"
                        >
                          <Trash2 aria-hidden="true" />
                          Revoke key
                        </DropdownMenuItem>
                      </>
                    ) : null}
                  </DropdownMenuContent>
                </DropdownMenu>
              </div>
            );
          }}
        />
      </div>
    </TooltipProvider>
  );
}
