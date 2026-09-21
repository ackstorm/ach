// TopKeys.test.tsx — jsdom smoke/contract suite for the STATS-07 top-keys panel.
//
// Asserts the locked section label + column copy, a representative ranked row
// (alias + masked-id fallback + formatted figures), the empty state, and the
// per_key_spend coming-soon gate.

import { describe, expect, it } from 'vitest';
import { fireEvent, render } from '@testing-library/react';

import type { StatsCapabilities, StatsKeyRow } from '@/lib/api-types';
import { TopKeys } from './TopKeys';

const CAPS: StatsCapabilities = {
  token_split: true,
  per_model_last_used: true,
  deltas: true,
  per_key_spend: true,
};

const KEYS: StatsKeyRow[] = [
  { id: 'key-abc123def456', key_alias: 'prod-key', requests: 1000, spend: 9.5, spend_pct: 0.7 },
  { id: 'key-zzz999yyy888', key_alias: null, requests: 250, spend: 2.5, spend_pct: 0.3 },
];

describe('TopKeys', () => {
  it('renders the section label + summary + column copy', () => {
    const { getByText, getByRole } = render(
      <TopKeys keys={KEYS} capabilities={CAPS} />
    );
    expect(getByText('TOP API KEYS')).toBeInTheDocument();
    // At-a-glance summary headline (StatTile labels are plain text).
    expect(getByText('ACTIVE KEYS')).toBeInTheDocument();
    // Column headers are sort buttons — disambiguates REQUESTS/SPEND from the
    // summary labels of the same word.
    expect(getByRole('button', { name: /KEY/ })).toBeInTheDocument();
    expect(getByRole('button', { name: /REQUESTS/ })).toBeInTheDocument();
    expect(getByRole('button', { name: /SPEND/ })).toBeInTheDocument();
    expect(getByRole('button', { name: /% OF TOTAL/ })).toBeInTheDocument();
  });

  it('renders a representative ranked row (alias + figures)', () => {
    const { getByText } = render(<TopKeys keys={KEYS} capabilities={CAPS} />);
    expect(getByText('prod-key')).toBeInTheDocument();
    expect(getByText('$9.50')).toBeInTheDocument();
    expect(getByText('70.0%')).toBeInTheDocument(); // spend_pct 0.7 -> 70.0%
  });

  it('masks the key id when there is no alias (maskKey fallback)', () => {
    const { getByText } = render(<TopKeys keys={KEYS} capabilities={CAPS} />);
    // maskKey("key-zzz999yyy888") -> "key-…y888" (first4…last4)
    expect(getByText('key-…y888')).toBeInTheDocument();
  });

  it('renders the empty copy for no rows', () => {
    const { getByText } = render(<TopKeys keys={[]} capabilities={CAPS} />);
    expect(getByText('No usage in this range')).toBeInTheDocument();
  });

  it('renders the coming-soon copy when per_key_spend is false', () => {
    const caps: StatsCapabilities = { ...CAPS, per_key_spend: false };
    const { getByText } = render(<TopKeys keys={KEYS} capabilities={caps} />);
    expect(getByText('Coming soon')).toBeInTheDocument();
  });
});

const MIXED: StatsKeyRow[] = [
  ...KEYS,
  { id: 'key-idle1', key_alias: 'idle-one', requests: 0, spend: 0, spend_pct: 0 },
  { id: 'key-idle2', key_alias: 'idle-two', requests: 0, spend: 0, spend_pct: 0 },
];

describe('TopKeys idle filtering', () => {
  it('hides idle (0 req / $0) keys by default, with a count toggle', () => {
    const { getByText, queryByText } = render(
      <TopKeys keys={MIXED} capabilities={CAPS} />
    );
    expect(getByText('prod-key')).toBeInTheDocument();
    expect(queryByText('idle-one')).toBeNull();
    expect(getByText('Show idle keys (2)')).toBeInTheDocument();
  });

  it('reveals idle keys on toggle click and can hide them again', () => {
    const { getByText, queryByText } = render(
      <TopKeys keys={MIXED} capabilities={CAPS} />
    );
    fireEvent.click(getByText('Show idle keys (2)'));
    expect(getByText('idle-one')).toBeInTheDocument();
    fireEvent.click(getByText('Hide idle keys'));
    expect(queryByText('idle-one')).toBeNull();
  });

  it('shows the empty copy + toggle when ALL keys are idle', () => {
    const allIdle = MIXED.slice(2);
    const { getByText } = render(<TopKeys keys={allIdle} capabilities={CAPS} />);
    expect(getByText('No usage in this range')).toBeInTheDocument();
    expect(getByText('Show idle keys (2)')).toBeInTheDocument();
  });
});

describe('TopKeys column sort', () => {
  const labels = (container: HTMLElement): (string | null | undefined)[] =>
    Array.from(container.querySelectorAll('[data-slot="top-keys-row"]')).map(
      (r) => r.firstElementChild?.textContent
    );

  it('defaults to SPEND descending', () => {
    const { container } = render(<TopKeys keys={KEYS} capabilities={CAPS} />);
    expect(labels(container)[0]).toBe('prod-key'); // 9.5 > 2.5
  });

  it('toggles the active column to ascending on a header click', () => {
    const { container, getByRole } = render(
      <TopKeys keys={KEYS} capabilities={CAPS} />
    );
    // SPEND is the default-active (desc) column, so one click flips it to asc.
    fireEvent.click(getByRole('button', { name: /SPEND/ }));
    const order = labels(container);
    expect(order[order.length - 1]).toBe('prod-key'); // largest spend now last
  });

  it('sorts by REQUESTS when its header is clicked', () => {
    const { container, getByRole } = render(
      <TopKeys keys={KEYS} capabilities={CAPS} />
    );
    fireEvent.click(getByRole('button', { name: /REQUESTS/ })); // desc by requests
    expect(labels(container)[0]).toBe('prod-key'); // 1000 > 250
  });
});
