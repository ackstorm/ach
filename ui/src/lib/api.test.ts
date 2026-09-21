import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { apiFetch } from './api';

vi.mock('./on-unauthorized', () => ({ notifyUnauthorized: vi.fn() }));
import { notifyUnauthorized } from './on-unauthorized';

afterEach(() => {
  vi.restoreAllMocks();
});

describe('apiFetch never-throw contract', () => {
  test('a thrown fetch (network failure) maps to { status: 0, data: null }', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockRejectedValue(new TypeError('Failed to fetch')),
    );

    const result = await apiFetch('/api/session/me');

    expect(result).toEqual({ status: 0, data: null });
  });

  test('a 401 with a non-JSON body maps to { status: 401, data: null }', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({
        status: 401,
        // resp.json() rejects on a non-JSON body — apiFetch must swallow it.
        json: () => Promise.reject(new SyntaxError('Unexpected end of JSON input')),
      } as unknown as Response),
    );

    const result = await apiFetch('/api/session/me');

    expect(result).toEqual({ status: 401, data: null });
  });

  test('an AbortError is re-thrown (cancellation, not a fake network error)', async () => {
    // A TanStack-Query cancellation aborts the fetch; apiFetch must surface that
    // as a rejection so Query treats it as a cancellation, NOT flip the
    // never-throw contract into a fake { status: 0 } network error.
    const abortErr =
      typeof DOMException !== 'undefined'
        ? new DOMException('The operation was aborted.', 'AbortError')
        : Object.assign(new Error('aborted'), { name: 'AbortError' });
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(abortErr));

    await expect(apiFetch('/api/session/keys')).rejects.toThrow();
  });
});

describe('apiFetch mid-session 401 notification', () => {
  beforeEach(() => {
    vi.mocked(notifyUnauthorized).mockClear();
  });

  const resp401 = () =>
    ({
      status: 401,
      json: () => Promise.reject(new SyntaxError('no body')),
    }) as unknown as Response;

  test('a 401 on a /api/session/* URL notifies the unauthorized handler', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(resp401()));
    await apiFetch('/api/session/models');
    expect(notifyUnauthorized).toHaveBeenCalledTimes(1);
  });

  test('a 401 on a non-session URL does NOT notify', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(resp401()));
    await apiFetch('/api/config');
    expect(notifyUnauthorized).not.toHaveBeenCalled();
  });

  test('a 200 on a /api/session/* URL does NOT notify', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({
        status: 200,
        json: () => Promise.resolve({ ok: true }),
      } as unknown as Response),
    );
    await apiFetch('/api/session/models');
    expect(notifyUnauthorized).not.toHaveBeenCalled();
  });
});
