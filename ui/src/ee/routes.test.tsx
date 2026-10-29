// SPDX-License-Identifier: LicenseRef-ACKstorm-EE
import { eeRoutes, eeTitles } from './routes';

test('every Enterprise route has a page title', () => {
  expect(eeTitles['/analytics/workloads']).toBe('Workloads');
  for (const route of eeRoutes) expect(eeTitles[`/${route.path}`]).toBeTruthy();
});
