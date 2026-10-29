import { afterEach, expect, it } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { UsageByModel } from './UsageByModel';

afterEach(() => { cleanup(); window.localStorage.clear(); });
const row = (model: string, spend: number, requests = 3, total_tokens = 0) => ({ model, spend, requests, spend_pct: spend / 100, input_tokens: 0, output_tokens: 0, total_tokens, cache_read_tokens: 0, last_used: null });
const models = [...Array.from({ length: 12 }, (_, i) => row(`m${i}`, 20 - i)), row('free-a', 0), row('free-b', 0.001)];
const names = (c: HTMLElement) => [...c.querySelectorAll('.hrow')].map((r) => r.firstChild?.textContent);

it('Cost: hides $0 rows with a count, caps to the limit, one value + share', () => {
  const { container } = render(<UsageByModel models={models} />);
  expect(screen.getByRole('heading', { name: 'Usage by model' })).toBeInTheDocument();
  expect(container.querySelectorAll('.hrow')).toHaveLength(10);
  expect(screen.queryByText('free-a')).toBeNull();
  expect(screen.getByText('m0')).toHaveAttribute('title', 'm0'); // full name on hover when the column caps
  expect(screen.getByText('2 models with no recorded spend hidden.')).toBeInTheDocument();
  expect(screen.getByText('$20.00 · 11.5%')).toBeInTheDocument(); // share of the shown list (174), computed in Bars
  expect(screen.queryByRole('button', { name: 'Next' })).toBeNull();
});

it('Tokens and Calls reorder, relabel, hide their own zero rows and persist', () => {
  const few = [row('a', 5, 10, 0), row('b', 1, 30, 3_000_000), row('c', 0, 0, 1_000_000)];
  const { container } = render(<UsageByModel models={few} />);
  expect(names(container)).toEqual(['a', 'b']);
  fireEvent.click(screen.getByRole('button', { name: 'Tokens' }));
  expect(names(container)).toEqual(['b', 'c']);
  expect(screen.getByText('3M · 75.0%')).toBeInTheDocument();
  expect(screen.getByText('1 model with no recorded tokens hidden.')).toBeInTheDocument();
  expect(screen.getByText('Public model alias · share of tokens')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Calls' }));
  expect(names(container)).toEqual(['b', 'a']);
  expect(screen.getByText('30 · 75.0%')).toBeInTheDocument();
  expect(window.localStorage.getItem('console.select.usage-by-model')).toBe('Calls');
  cleanup();
  const again = render(<UsageByModel models={few} />);
  expect(screen.getByRole('button', { name: 'Calls' })).toHaveAttribute('aria-pressed', 'true');
  expect(names(again.container)).toEqual(['b', 'a']);
});

it('paged shows the pager only past 10 rows', () => {
  render(<UsageByModel models={models} paged />);
  expect(screen.getByText('1–10 of 12')).toBeInTheDocument();
  cleanup();
  render(<UsageByModel models={models.slice(0, 10)} paged />);
  expect(screen.queryByRole('button', { name: 'Next' })).toBeNull();
});
