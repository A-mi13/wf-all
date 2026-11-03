import type { AnyHandler } from 'msw';
import { setupWorker } from 'msw/browser';
import { createOpenApiHttp } from 'openapi-msw';
import type { paths } from '../api/gen/admin';
import { defaultHandlers } from '../test/defaults';
import { TEST_API } from './base';

// Режим shots (спека §5.4): тот же внешний вид, что у src/test/server.ts (TEST_API, http, server с
// use/resetHandlers/listen/close), но поверх сервис-воркера MSW в браузере. Плагин wf-shots
// (vite.shots.ts) подменяет им src/test/server.ts — фикстуры (sectionsServer, geoServer…) работают без
// правок. Неописанный запрос к API — console.error и счётчик window.__wfShots.unhandled: скрипт
// снимков (scripts/admin-shots.mjs) по ним падает.

declare global {
  interface Window {
    __wfShots?: { unhandled: number };
  }
}

export { TEST_API };
export const http = createOpenApiHttp<paths>({ baseUrl: TEST_API });

const worker = setupWorker(...defaultHandlers(http));

const isApi = (url: string) => new URL(url).pathname.startsWith('/v1/');

worker.events.on('request:unhandled', ({ request }) => {
  // модули, стили и шрифты dev-сервера идут мимо моков — это не ошибка
  if (!isApi(request.url)) return;
  const stats = (window.__wfShots ??= { unhandled: 0 });
  stats.unhandled++;
  console.error(`MSW: неописанный запрос — ${request.method} ${request.url}`);
});

// Регистрация воркера — до монтирования приложения: иначе первые запросы me уйдут мимо моков.
export async function start(): Promise<void> {
  window.__wfShots ??= { unhandled: 0 };
  await worker.start({ quiet: true, onUnhandledFrame: 'bypass' });
}

export const server = {
  use: (...handlers: AnyHandler[]) => worker.use(...handlers),
  resetHandlers: (...handlers: AnyHandler[]) => worker.resetHandlers(...handlers),
  listen: start,
  close: () => worker.stop(),
  events: worker.events,
};
