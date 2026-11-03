import { setupServer } from 'msw/node';
import { createOpenApiHttp } from 'openapi-msw';
import type { paths } from '../api/gen/admin';
import { TEST_API } from './base';
import { defaultHandlers } from './defaults';

// Моки типизированы из того же контракта, что и сервер. Тест ставит свои через
// server.use(http.get('/v1/staff/me', …)); неописанный запрос роняет тест (src/test/setup.ts).
// Общие ответы любого раздела — defaultHandlers (src/test/defaults.ts). Режим shots подменяет этот
// модуль браузерным src/shots/server.ts с тем же видом (vite.shots.ts) — фикстуры не переписываются.
export { TEST_API };
export const http = createOpenApiHttp<paths>({ baseUrl: TEST_API });
export const server = setupServer(...defaultHandlers(http));
