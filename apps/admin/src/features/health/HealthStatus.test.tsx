import { screen } from '@testing-library/react';
import { HttpResponse, http as mswHttp } from 'msw';
import { expect, test } from 'vitest';
import { renderWithProviders } from '../../test/render';
import { TEST_API, server } from '../../test/server';
import { HealthStatus } from './HealthStatus';

test('API отвечает — показываем «API работает»', async () => {
  renderWithProviders(<HealthStatus />);
  expect(await screen.findByText('API работает')).toBeInTheDocument();
});

test('API вернул ошибку — показываем «API недоступен»', async () => {
  server.use(
    mswHttp.get(`${TEST_API}/v1/health`, () =>
      HttpResponse.json(
        { type: 'about:blank', title: 'Internal Server Error', status: 500, code: 'internal' },
        { status: 500, headers: { 'Content-Type': 'application/problem+json' } },
      ),
    ),
  );
  renderWithProviders(<HealthStatus />);
  expect(await screen.findByText('API недоступен')).toBeInTheDocument();
});
