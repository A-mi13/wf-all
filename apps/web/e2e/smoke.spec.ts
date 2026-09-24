// Серверный компонент страницы проверяется в браузере: Vitest его не рендерит.
import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';

const ru = JSON.parse(readFileSync(new URL('../messages/ru.json', import.meta.url), 'utf8'));

test('главная отдаёт заголовок из локали', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByRole('heading', { level: 1 })).toHaveText(ru.app.title);
});
