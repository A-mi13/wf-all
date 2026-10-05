import { screen } from '@testing-library/react';
import { HttpResponse, http as mswHttp } from 'msw';
import { expect, test } from 'vitest';
import { renderWithProviders } from '../../test/render';
import { TEST_API, server } from '../../test/server';
import { ConnectionStatus } from './ConnectionStatus';

const problem = (status: number, title: string, code: string) =>
  HttpResponse.json(
    { type: 'about:blank', title, status, code },
    { status, headers: { 'Content-Type': 'application/problem+json' } },
  );

test('API ответил страницей городов — «Связь с API есть»; запрос — listCities с limit=1', async () => {
  let search: string | null = null;
  server.use(
    mswHttp.get(`${TEST_API}/v1/cities`, ({ request }) => {
      search = new URL(request.url).search;
      return HttpResponse.json({ items: [], next_cursor: null });
    }),
  );
  renderWithProviders(<ConnectionStatus />);
  expect(await screen.findByText('Связь с API есть')).toBeInTheDocument();
  // одна запись — самый дешёвый запрос справочника
  expect(search).toBe('?limit=1');
});

test('пока ответа нет — «Проверяем связь с API…»', async () => {
  renderWithProviders(<ConnectionStatus />);
  expect(screen.getByRole('status')).toHaveTextContent('Проверяем связь с API…');
  // дождаться ответа мока по умолчанию, чтобы запрос не пережил тест
  expect(await screen.findByText('Связь с API есть')).toBeInTheDocument();
});

test('API вернул ошибку — «Нет связи с API»', async () => {
  server.use(
    mswHttp.get(`${TEST_API}/v1/cities`, () => problem(500, 'Internal Server Error', 'internal')),
  );
  renderWithProviders(<ConnectionStatus />);
  expect(await screen.findByText('Нет связи с API')).toBeInTheDocument();
});

test('сеть недоступна — «Нет связи с API»', async () => {
  server.use(mswHttp.get(`${TEST_API}/v1/cities`, () => HttpResponse.error()));
  renderWithProviders(<ConnectionStatus />);
  expect(await screen.findByText('Нет связи с API')).toBeInTheDocument();
});
