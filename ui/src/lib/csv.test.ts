// csv.test.ts — node-env unit tests for the pure CSV serializer.
import { describe, expect, it } from 'vitest';

import { toCsv } from './csv';

const COLS = [
  { key: 'name' as const, header: 'NAME' },
  { key: 'n' as const, header: 'N' },
];

describe('toCsv', () => {
  it('serializes header + rows with trailing newline', () => {
    expect(toCsv([{ name: 'a', n: 1 }], COLS)).toBe('NAME,N\na,1\n');
  });

  it('quotes cells containing comma, quote, or newline (RFC 4180)', () => {
    expect(toCsv([{ name: 'a,"b"\nc', n: 2 }], COLS)).toBe(
      'NAME,N\n"a,""b""\nc",2\n'
    );
  });

  it("defuses spreadsheet formula injection with a leading '", () => {
    expect(toCsv([{ name: '=SUM(A1)', n: 3 }], COLS)).toBe(
      "NAME,N\n'=SUM(A1),3\n"
    );
  });

  it('renders null/undefined as empty cells', () => {
    expect(toCsv([{ name: null, n: undefined }], COLS)).toBe('NAME,N\n,\n');
  });
});
