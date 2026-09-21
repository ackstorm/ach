// SiteFooter.test.tsx — vitest suite for the shared site footer (jsdom).
//
// Asserts the always-present © line (year + brand) and the real-links-only
// behavior: Privacy Policy / Terms of Service anchors render ONLY when their
// targets are set in config.links; when neither is set, the © line stands alone
// (no dead anchors).

import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, render, screen } from '@testing-library/react';

import type { AppConfig } from '@/lib/api-types';
import { SiteFooter } from './SiteFooter';

// A minimal valid AppConfig with overridable links.
function makeConfig(overrides: Partial<AppConfig> = {}): AppConfig {
  return {
    brand: 'ACKStorm Platform',
    brand_short: 'LiteLLM',
    tagline: '',
    accent_segment: '-auth',
    public_host: '',
    chat_public_url: '',
    providers: [],
    links: {},
    ...overrides,
  };
}

afterEach(() => {
  cleanup();
});

describe('SiteFooter — copyright line', () => {
  it('renders "© {year} {brand}. All rights reserved." with the config brand', () => {
    const year = new Date().getFullYear();
    render(<SiteFooter config={makeConfig({ brand: 'ACKStorm Platform' })} />);
    expect(
      screen.getByText(`© ${year} ACKStorm Platform. All rights reserved.`),
    ).toBeInTheDocument();
  });

  it('falls back to "alitellm-auth" when brand is empty', () => {
    const year = new Date().getFullYear();
    render(<SiteFooter config={makeConfig({ brand: '' })} />);
    expect(
      screen.getByText(`© ${year} alitellm-auth. All rights reserved.`),
    ).toBeInTheDocument();
  });
});

describe('SiteFooter — links (real-links-only)', () => {
  it('renders BOTH links when privacy + terms are set', () => {
    render(
      <SiteFooter
        config={makeConfig({
          links: {
            privacy: 'https://example.com/privacy',
            terms: 'https://example.com/terms',
          },
        })}
      />,
    );
    const privacy = screen.getByRole('link', { name: 'Privacy Policy' });
    const terms = screen.getByRole('link', { name: 'Terms of Service' });
    expect(privacy).toHaveAttribute('href', 'https://example.com/privacy');
    expect(privacy).toHaveAttribute('target', '_blank');
    expect(privacy).toHaveAttribute('rel', 'noopener noreferrer');
    expect(terms).toHaveAttribute('href', 'https://example.com/terms');
  });

  it('renders NEITHER link when both are absent', () => {
    render(<SiteFooter config={makeConfig({ links: {} })} />);
    expect(
      screen.queryByRole('link', { name: 'Privacy Policy' }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole('link', { name: 'Terms of Service' }),
    ).not.toBeInTheDocument();
  });

  it('renders only the link whose target is set', () => {
    render(
      <SiteFooter
        config={makeConfig({ links: { privacy: 'https://example.com/privacy' } })}
      />,
    );
    expect(
      screen.getByRole('link', { name: 'Privacy Policy' }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole('link', { name: 'Terms of Service' }),
    ).not.toBeInTheDocument();
  });
});
