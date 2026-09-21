import { render, screen } from '@testing-library/react';
import { Button } from './button';
test('renders a button with its label', () => {
  render(<Button>save</Button>);
  expect(screen.getByRole('button', { name: 'save' })).toBeInTheDocument();
});
