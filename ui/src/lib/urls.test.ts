import { describe, expect, it } from 'vitest';

import { deriveSubdomainUrl } from './urls';

describe('deriveSubdomainUrl', () => {
  it('swaps the leading api. label for the given subdomain', () => {
    expect(deriveSubdomainUrl('https://api.acme.ai', 'chat')).toBe('https://chat.acme.ai');
  });

  it('prepends the subdomain to a bare host without an api. prefix', () => {
    expect(deriveSubdomainUrl('https://gateway.acme.ai', 'chat')).toBe(
      'https://chat.gateway.acme.ai',
    );
  });

  it('preserves the protocol', () => {
    expect(deriveSubdomainUrl('http://api.local.test', 'chat')).toBe('http://chat.local.test');
  });

  it('falls back to a neutral placeholder on absent/unparseable input', () => {
    expect(deriveSubdomainUrl(undefined, 'chat')).toBe('https://chat.your-domain.example');
    expect(deriveSubdomainUrl('not-a-url', 'chat')).toBe('https://chat.your-domain.example');
  });
});
