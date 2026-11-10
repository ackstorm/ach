// HowToCli.test.tsx — jsdom suite for the ach-cli guide.

import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router';

import { HowToCli } from './HowToCli';

function renderPage() {
  return render(<MemoryRouter><HowToCli /></MemoryRouter>);
}

afterEach(() => {
  vi.clearAllMocks();
  cleanup();
});

describe('HowToCli', () => {
  it('has one section per contents entry', () => {
    renderPage();
    const toc = screen.getByRole('navigation', { name: 'On this page' });
    const entries = [...toc.querySelectorAll('button')].map((b) => b.textContent);
    expect(entries).toEqual(['Install & update', 'Sign in & profiles', 'Environments', 'Environment keys', 'Local packages']);
    for (const title of entries) expect(screen.getByRole('heading', { name: title! })).toBeInTheDocument();
  });

  it('writes the environment command and flag, never the env alias', () => {
    const { container } = renderPage();
    expect(container.textContent).toMatch(/ach-cli environment hydrate/);
    expect(container.textContent).not.toMatch(/ach-cli env(?!ironment)/);
    expect(container.textContent).not.toMatch(/--env(?!ironment)/);
  });

  it('signs in to this console origin and copies exactly it', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    const { container } = renderPage();
    const origin = window.location.origin;
    expect(container.textContent).toContain(`ach-cli login ${origin}`);
    fireEvent.click(screen.getByRole('button', { name: 'Copy' }));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith(origin));
  });

  it('offers Linux & macOS and Windows install tabs, both checked or versioned', () => {
    const { container } = renderPage();
    expect(screen.getByRole('tab', { name: 'Linux & macOS' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'Windows' })).toBeInTheDocument();
    expect(container.textContent).toMatch(/checksums\.txt/);
  });

  it('renders every snippet in the shared dark code block and never a real key', () => {
    const { container } = renderPage();
    expect(container.querySelectorAll('.codeblock').length).toBeGreaterThan(0);
    expect(container.querySelectorAll('pre').length).toBe(container.querySelectorAll('.codeblock pre').length);
    expect(container.textContent).not.toMatch(/ek-[A-Za-z0-9]{8,}/);
  });

  it('points MCP sign-in at the How-to page instead of repeating it', () => {
    renderPage();
    expect(screen.getByRole('link', { name: /MCP servers/ })).toHaveAttribute('href', '/howto');
  });
});
