import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { CodeBlock } from './CodeBlock';

afterEach(cleanup);

it('renders the code in the dark block with a caption and copies the exact text', async () => {
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
  const { container } = render(<CodeBlock code={'echo "hi" # note'} caption="shell" />);
  expect(container.firstElementChild).toHaveClass('codeblock');
  expect(screen.getByText('shell')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'copy' }));
  await waitFor(() => expect(writeText).toHaveBeenCalledWith('echo "hi" # note'));
});

it('inline: one row with a $ prompt that is not copied', async () => {
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
  const { container } = render(<CodeBlock inline code="ach-cli env hydrate x --dir ./x" />);
  expect(container.firstElementChild).toHaveClass('codeblock', 'inline');
  expect(container.querySelector('.prompt')).toHaveTextContent('$');
  fireEvent.click(screen.getByRole('button', { name: 'copy' }));
  await waitFor(() => expect(writeText).toHaveBeenCalledWith('ach-cli env hydrate x --dir ./x'));
});
