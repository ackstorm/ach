// Login.test.tsx — the single, centered sign-in landing (Task 2.5).
//
// Asserts the load-bearing contract ported from src/ui/login.js:
//   • the SSO CTA is a real link to the FIXED /platform/console/session/login?next=%2F literal
//     (the ONLY redirect trigger, T-09-17) with its locked "Continue with SSO"
//     label as the accessible name;
//   • the two-tone brand lockup renders (base + green accent span);
//   • the "Sign in" <h1> heading copy is present;
//   • the BACKED BY provider chips render from config.providers AND are NOT
//     aria-hidden (WR-04 — they must stay in the a11y tree).

import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, render, screen, within } from '@testing-library/react';
import { Login } from './Login';
import { DEFAULT_CONFIG } from '@/stores/config';
import type { AppConfig } from '@/lib/api-types';

afterEach(() => cleanup());

const PROVIDERS = [{ label: 'Google' }, { label: 'Dex' }, { label: 'OIDC' }];

describe('Login', () => {
  it('CTA links to the FIXED /platform/console/session/login?next=%2F literal', () => {
    render(<Login config={DEFAULT_CONFIG} />);
    const cta = screen.getByRole('link', { name: 'Continue with SSO' });
    expect(cta).toHaveAttribute('href', '/platform/console/session/login?next=%2F');
  });

  it('renders the two-tone brand lockup (base + accent)', () => {
    render(
      <Login config={{ ...DEFAULT_CONFIG, brand: 'alitellm-auth', accent_segment: '-auth' }} />,
    );
    // BrandLockup appears twice (topbar + card); both halves must be present.
    expect(screen.getAllByText('alitellm').length).toBeGreaterThan(0);
    expect(screen.getAllByText('-auth').length).toBeGreaterThan(0);
  });

  it('renders the whole brand with no accent span when accent_segment is empty (DEFAULT_CONFIG)', () => {
    render(<Login config={DEFAULT_CONFIG} />);
    expect(screen.getAllByText('ACH').length).toBeGreaterThan(0);
  });

  it('renders the "Sign in" h1 heading', () => {
    render(<Login config={DEFAULT_CONFIG} />);
    expect(
      screen.getByRole('heading', { level: 1, name: 'Sign in' }),
    ).toBeInTheDocument();
  });

  it('renders a BACKED BY provider chip per config.providers entry', () => {
    render(<Login config={{ ...DEFAULT_CONFIG, providers: PROVIDERS }} />);
    expect(screen.getByText('Google')).toBeInTheDocument();
    expect(screen.getByText('Dex')).toBeInTheDocument();
    expect(screen.getByText('OIDC')).toBeInTheDocument();
    expect(screen.getByText('BACKED BY')).toBeInTheDocument();
  });

  it('renders no BACKED BY block for DEFAULT_CONFIG (no providers)', () => {
    render(<Login config={DEFAULT_CONFIG} />);
    expect(screen.queryByText('BACKED BY')).toBeNull();
  });

  it('provider chips are NOT aria-hidden (WR-04 — in the a11y tree)', () => {
    render(<Login config={{ ...DEFAULT_CONFIG, providers: PROVIDERS }} />);
    // Each chip label and its nearest chip wrapper must not be aria-hidden, and
    // must not sit inside an aria-hidden ancestor (which would hide it from AT).
    for (const label of ['Google', 'Dex', 'OIDC']) {
      const el = screen.getByText(label);
      expect(el.closest('[aria-hidden="true"]')).toBeNull();
    }
  });

  it('renders provider chips from a custom config.providers list', () => {
    const config: AppConfig = {
      ...DEFAULT_CONFIG,
      providers: [{ label: 'Okta' }, { label: 'Auth0' }],
    };
    render(<Login config={config} />);
    expect(screen.getByText('Okta')).toBeInTheDocument();
    expect(screen.getByText('Auth0')).toBeInTheDocument();
    // The removed defaults must be gone.
    expect(screen.queryByText('Google')).toBeNull();
  });

  it('renders config.tagline in the topbar when set, omits it when empty', () => {
    const { rerender } = render(<Login config={DEFAULT_CONFIG} />);
    // DEFAULT_CONFIG.tagline === '' -> not rendered.
    expect(screen.queryByText('Self-service LiteLLM keys')).toBeNull();

    rerender(
      <Login
        config={{ ...DEFAULT_CONFIG, tagline: 'Self-service LiteLLM keys' }}
      />,
    );
    expect(
      screen.getByText('Self-service LiteLLM keys'),
    ).toBeInTheDocument();
  });

  it('renders Docs/Status/Support nav only for configured links (real-links-only)', () => {
    // DEFAULT_CONFIG.links === {} -> no resource nav anchors at all.
    const { rerender } = render(<Login config={DEFAULT_CONFIG} />);
    expect(screen.queryByRole('link', { name: 'Docs' })).toBeNull();

    rerender(
      <Login
        config={{
          ...DEFAULT_CONFIG,
          links: { docs: 'https://docs.example.com' },
        }}
      />,
    );
    const docs = screen.getByRole('link', { name: 'Docs' });
    expect(docs).toHaveAttribute('href', 'https://docs.example.com');
    // Status/Support targets were not supplied -> still dropped.
    expect(screen.queryByRole('link', { name: 'Status' })).toBeNull();
    expect(screen.queryByRole('link', { name: 'Support' })).toBeNull();
  });

  it('uses config.brand_short in the sub copy', () => {
    render(<Login config={DEFAULT_CONFIG} />);
    const card = screen.getByRole('main');
    expect(
      within(card).getByText(/manage your/),
    ).toHaveTextContent('ACH virtual keys');
  });
});
