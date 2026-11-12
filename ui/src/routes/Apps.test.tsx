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
    expect(workspace).toHaveTextContent('ChatEvery modelCoder');
    const grafana = screen.getByRole('link', { name: /Grafana/ });
    expect(grafana).toHaveAttribute('href', 'https://grafana.acme.ai');
    expect(grafana).toHaveAttribute('target', '_blank');
    expect(grafana.querySelector('svg')).not.toBeNull(); // unknown icon falls back
  });
});
