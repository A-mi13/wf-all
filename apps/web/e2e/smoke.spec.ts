// Серверный компонент страницы проверяется в браузере: Vitest его не рендерит.
import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';

const ru = JSON.parse(readFileSync(new URL('../messages/ru.json', import.meta.url), 'utf8'));

// Запрос проверки связи браузер шлёт на свой origin (/v1 проксирует Next) — перехватываем его.
const isListCities = (url: URL) => url.pathname === '/v1/cities';

test('главная отдаёт заголовок из локали', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByRole('heading', { level: 1 })).toHaveText(ru.app.title);
});

test('главная показывает связь с API по ответу listCities (limit=1)', async ({ page }) => {
  let limit: string | null = null;
  await page.route(isListCities, async (route) => {
    limit = new URL(route.request().url()).searchParams.get('limit');
    await route.fulfill({ json: { items: [], next_cursor: null } });
  });
  await page.goto('/');
  await expect(page.getByRole('status')).toHaveText(ru.connection.ok);
  expect(limit).toBe('1');
});

test('API отвечает ошибкой — главная показывает «нет связи»', async ({ page }) => {
  await page.route(isListCities, (route) =>
    route.fulfill({
      status: 503,
      contentType: 'application/problem+json',
      body: JSON.stringify({
        type: 'about:blank',
        title: 'Service Unavailable',
        status: 503,
        code: 'internal',
      }),
    }),
  );
  await page.goto('/');
  // React Query в приложении повторяет запрос 3 раза с паузами 1 + 2 + 4 с — ждём дольше 5 с по умолчанию
  await expect(page.getByRole('status')).toHaveText(ru.connection.down, { timeout: 20_000 });
});
