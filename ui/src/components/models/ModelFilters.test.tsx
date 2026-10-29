import { describe, it, expect } from 'vitest';
import {
  applyModelFilters,
  applyModeFilter,
  modelModeOptions,
} from '@/components/models/ModelFilters';

const row = (over: Partial<Record<string, unknown>>) => ({
  name: 'm', providers: [], mode: 'chat',
  max_input_tokens: null, max_output_tokens: null,
  input_cost_per_token: null, output_cost_per_token: null,
  supports_vision: false, supports_function_calling: false,
  supports_reasoning: false, supports_web_search: false, ...over,
});

describe('applyModelFilters', () => {
  it('keeps only rows matching every active capability', () => {
    const rows = [row({ supports_vision: true }), row({})];
    expect(applyModelFilters(rows as never, new Set(['vision']))).toHaveLength(1);
    expect(applyModelFilters(rows as never, new Set())).toHaveLength(2);
  });

  it('requires ALL active caps (AND semantics)', () => {
    const rows = [
      row({ supports_vision: true, supports_reasoning: true }),
      row({ supports_vision: true }),
    ];
    expect(
      applyModelFilters(rows as never, new Set(['vision', 'reasoning'])),
    ).toHaveLength(1);
  });
});

describe('modelModeOptions', () => {
  it('lists distinct mode buckets in user-facing order (chat before embeddings)', () => {
    const rows = [
      row({ mode: 'embedding' }),
      row({ mode: 'chat' }),
      row({ mode: 'completion' }), // buckets into "chat" — deduped
    ];
    expect(modelModeOptions(rows as never)).toEqual([
      { key: 'chat', label: 'Chat' },
      { key: 'embeddings', label: 'Embeddings' },
    ]);
  });

  it('title-cases an unknown mode and sorts it after the known ones', () => {
    const rows = [row({ mode: 'rerank' }), row({ mode: 'chat' })];
    expect(modelModeOptions(rows as never)).toEqual([
      { key: 'chat', label: 'Chat' },
      { key: 'rerank', label: 'Rerank' },
    ]);
  });
});

describe('applyModeFilter', () => {
  it('keeps rows whose mode bucket is active (OR semantics)', () => {
    const rows = [
      row({ name: 'c', mode: 'chat' }),
      row({ name: 'e', mode: 'embedding' }),
      row({ name: 'i', mode: 'image_generation' }),
    ];
    expect(applyModeFilter(rows as never, new Set(['chat', 'image'])).map((r) => r.name)).toEqual(
      ['c', 'i'],
    );
    expect(applyModeFilter(rows as never, new Set())).toHaveLength(3);
  });
});
