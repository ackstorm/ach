import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';

import type { SessionMe } from '@/lib/api-types';
import { useSessionStore } from '@/stores/session';
import { Apps } from './Apps';

afterEach(cleanup);

describe('Apps', () => {
  it('groups tiles in first-seen order and opens each in a new tab', () => {
    useSessionStore.setState({ me: { apps: [
      { group: 'Workspace', name: 'Chat', url: 'https://chat.acme.ai', icon: 'message-circle', subtitle: 'Every model' },
      { group: 'Admin', name: 'Grafana', url: 'https://grafana.acme.ai', icon: 'not-a-glyph' },
      { group: 'Workspace', name: 'Coder', url: 'https://coder.acme.ai', icon: 'constructor' },
    ] } as unknown as SessionMe });
    render(<Apps />);
    expect(screen.getAllByRole('heading', { level: 2 }).map((h) => h.textContent)).toEqual(['Workspace', 'Admin']);
    const workspace = screen.getByRole('region', { name: 'Workspace' });
    expect(workspace).toHaveTextContent('ChatEvery modelchat.acme.aiCodercoder.acme.ai');
    expect(screen.getByRole('link', { name: /Chat/ }).getAttribute('style')).toContain('oklch(from var(--primary) l c 322)');
    const grafana = screen.getByRole('link', { name: /Grafana/ });
    expect(grafana).toHaveAttribute('href', 'https://grafana.acme.ai');
    expect(grafana).toHaveAttribute('target', '_blank');
    expect(grafana.querySelectorAll('svg')).toHaveLength(2); // unknown icon falls back, plus the arrow
    expect(grafana.getAttribute('style')).toContain('--app: var(--primary)');
  });
});
