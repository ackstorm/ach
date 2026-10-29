import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';

import { StatusPill } from './StatusPill';
import { Tag } from './Tag';

afterEach(cleanup);

describe('StatusPill', () => {
  it.each(['ok', 'warn', 'danger', 'info', 'neutral', 'muted'] as const)('tone %s gives .pill.%s', (tone) => {
    render(<StatusPill tone={tone}>Some label</StatusPill>);
    const pill = screen.getByText('Some label'); // as given; CSS uppercases it
    expect(pill).toHaveClass('pill', tone);
  });

  it('keeps an extra class', () => {
    render(<StatusPill tone="ok" className="c-status">Success</StatusPill>);
    expect(screen.getByText('Success')).toHaveClass('pill', 'ok', 'c-status');
  });
});

describe('Tag', () => {
  it('renders .tag, dashed with dim', () => {
    render(<><Tag>openai</Tag><Tag dim>0 skills</Tag></>);
    expect(screen.getByText('openai')).toHaveClass('tag');
    expect(screen.getByText('openai')).not.toHaveClass('dim');
    expect(screen.getByText('0 skills')).toHaveClass('tag', 'dim');
  });
});
