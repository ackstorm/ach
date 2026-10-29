// skeleton.tsx — per-panel loading shimmer primitive, ported from src/ui/skeleton.js.
//
// skeleton.js rendered a shape-matched placeholder occupying the SAME footprint
// as the real panel while data resolved: a --border-tinted, 16px-radius block
// animating the inherited `pulse` keyframe (a subtle --surface <-> --border
// pulse). It exposed shape variants — card (96px), chart (240px), bar (14px
// pill), table-rows (a stack of row bars) — all aria-hidden (purely decorative).
//
// This ports that faithfully:
//   * the `pulse` animation -> Tailwind's built-in `animate-pulse` utility
//     (same opacity pulse intent), so NO new keyframe is added to index.css;
//   * the --border tint + 12px radius -> bg-border rounded-xl (matches the
//     content cards it stands in for);
//   * the same variant footprints (card/chart/bar/table-rows);
//   * aria-hidden on every block (decorative, threat T-13-04 parity).
// A free-form `className` is also accepted so callers can size/shape a one-off
// placeholder without a dedicated variant (the task's size/shape-via-className).

import * as React from 'react';

import { cn } from '@/lib/utils';

export type SkeletonVariant = 'card' | 'chart' | 'bar' | 'table-rows';

// Per-variant footprint, ported from skeleton.js's SKELETON_CSS:
//   card  -> 96px tall   chart -> 240px tall   bar -> 14px pill.
const VARIANT_CLASS: Record<Exclude<SkeletonVariant, 'table-rows'>, string> = {
  card: 'h-24', // 96px
  chart: 'h-60', // 240px
  bar: 'h-3.5 rounded-full', // 14px pill
};

export interface SkeletonProps extends React.ComponentProps<'div'> {
  /** Shape variant. Defaults to "card" (matches skeleton.js's default). */
  variant?: SkeletonVariant;
  /** Row count for the "table-rows" variant. Defaults to 5 (matches skeleton.js). */
  rows?: number;
}

/**
 * Skeleton — a shape-matched shimmer placeholder.
 *
 *   <Skeleton />                      a 96px card block
 *   <Skeleton variant="chart" />      a 240px chart block
 *   <Skeleton variant="bar" />        a 14px pill bar
 *   <Skeleton variant="table-rows" rows={3} />   a stack of row bars
 *   <Skeleton className="h-8 w-32" /> a free-form sized block
 *
 * The block(s) animate the built-in `animate-pulse` over a --border tint, the
 * same decorative shimmer skeleton.js produced. Every node is aria-hidden.
 */
export function Skeleton({ variant = 'card', rows = 5, className, ...props }: SkeletonProps) {
  if (variant === 'table-rows') {
    const count = Number.isFinite(rows) && rows > 0 ? Math.floor(rows) : 5;
    return (
      <div
        data-slot="skeleton"
        data-variant="table-rows"
        aria-hidden="true"
        className={cn('flex w-full flex-col gap-3', className)}
        {...props}
      >
        {Array.from({ length: count }).map((_, i) => (
          <div key={i} className="h-4 animate-pulse rounded-full bg-border" />
        ))}
      </div>
    );
  }

  return (
    <div
      data-slot="skeleton"
      data-variant={variant}
      aria-hidden="true"
      className={cn('w-full animate-pulse rounded-xl bg-border', VARIANT_CLASS[variant], className)}
      {...props}
    />
  );
}
