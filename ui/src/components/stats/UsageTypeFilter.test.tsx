// UsageTypeFilter.test.tsx — vitest suite for the USAGE BREAKDOWN TYPE chips
// (jsdom). Covers the pure row filter and the single-select chip behaviour.

import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';

import type { StatsModelRow } from '@/lib/api-types';
import { UsageTypeFilter, applyUsageTypeFilter } from './UsageTypeFilter';

function row(model: string | null): StatsModelRow {
  return {
    model,
    requests: 1,
    input_tokens: 0,
    output_tokens: 0,
    total_tokens: 0,
    cache_read_tokens: 0,
    spend: 0,
    spend_pct: null,
    last_used: null,
  };
}

const ROWS = [row('anthropic/claude-opus-5'), row('MCP: list_tools'), row('gemini-flash-latest')];

afterEach(cleanup);

describe('applyUsageTypeFilter', () => {
  it('passes every row through on "all"', () => {
    expect(applyUsageTypeFilter(ROWS, 'all')).toHaveLength(3);
  });

  it('keeps only non-MCP rows on "model"', () => {
    expect(applyUsageTypeFilter(ROWS, 'model').map((r) => r.model)).toEqual([
      'anthropic/claude-opus-5',
      'gemini-flash-latest',
    ]);
  });

  it('keeps only MCP tool rows on "mcp"', () => {
    expect(applyUsageTypeFilter(ROWS, 'mcp').map((r) => r.model)).toEqual(['MCP: list_tools']);
  });

  it('treats a null model name as a plain model row, not an MCP tool', () => {
    expect(applyUsageTypeFilter([row(null)], 'model')).toHaveLength(1);
    expect(applyUsageTypeFilter([row(null)], 'mcp')).toHaveLength(0);
  });

  it('returns an empty list when the window has no rows of that type', () => {
    expect(applyUsageTypeFilter([row('gemini-flash-latest')], 'mcp')).toEqual([]);
  });
});

describe('UsageTypeFilter', () => {
  it('marks exactly the active chip as pressed', () => {
    render(<UsageTypeFilter value="mcp" onChange={() => {}} />);

    expect(screen.getByRole('button', { name: 'MCP Tool' })).toHaveAttribute(
      'aria-pressed',
      'true',
    );
    expect(screen.getByRole('button', { name: 'All' })).toHaveAttribute('aria-pressed', 'false');
    expect(screen.getByRole('button', { name: 'Model' })).toHaveAttribute('aria-pressed', 'false');
  });

  it('emits the picked type on click', () => {
    const onChange = vi.fn();
    render(<UsageTypeFilter value="all" onChange={onChange} />);

    fireEvent.click(screen.getByRole('button', { name: 'Model' }));
    expect(onChange).toHaveBeenCalledWith('model');

    fireEvent.click(screen.getByRole('button', { name: 'MCP Tool' }));
    expect(onChange).toHaveBeenCalledWith('mcp');
  });

  it('renders all three chips even when the window holds a single row type', () => {
    render(<UsageTypeFilter value="all" onChange={() => {}} />);
    expect(screen.getAllByRole('button')).toHaveLength(3);
  });
});
