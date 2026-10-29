import { afterEach, expect, it } from 'vitest';
import { cleanup, fireEvent, render } from '@testing-library/react';
import { ChartFrame } from './ChartFrame';

afterEach(cleanup);

it('draws the Y labels with the given format and the first/last X labels, in both sizes', () => {
  const { container } = render(<ChartFrame max={10} format={(v) => `$${v}`} xLabels={['Oct 01', 'Oct 28']}>{(f) => <rect data-testid="mark" x={f.x(0)} y={f.y(10)} />}</ChartFrame>);
  const svgs = container.querySelectorAll('svg');
  expect(svgs).toHaveLength(2);
  const labels = [...svgs[0].querySelectorAll('text.chart-label')].map((t) => t.textContent);
  expect(labels).toEqual(['$0', '$5', '$10', 'Oct 01', 'Oct 28']);
  expect(svgs[0].querySelector('rect')).toHaveAttribute('y', '25');
});

it('shows the card of the point nearest the pointer and clears it on leave', () => {
  const tip = (i: number) => ({ title: `Day ${i}`, rows: [{ label: 'Calls', value: String(i * 10), color: 'red', y: i }] });
  const { container } = render(<ChartFrame max={10} format={String} xLabels={['a', 'b']} hover={{ n: 3, at: (i) => i / 2, tip }}>{() => null}</ChartFrame>);
  const svg = container.querySelector('svg.desktop-chart')!;
  // Rendered at exactly the viewBox size: client x == viewBox x.
  svg.getBoundingClientRect = () => ({ left: 0, top: 0, width: 820, height: 190, right: 820, bottom: 190, x: 0, y: 0, toJSON: () => ({}) });
  fireEvent.mouseMove(svg, { clientX: 440, clientY: 60 }); // the middle point sits at x 428
  expect(container.querySelector('.chart-tip')).toHaveTextContent('Day 1Calls10');
  expect(svg.querySelector('.chart-hover circle')).toHaveAttribute('cy', String(150 - 1 / 10 * 125));
  fireEvent.mouseLeave(svg);
  expect(container.querySelector('.chart-tip')).toBeNull();
});
