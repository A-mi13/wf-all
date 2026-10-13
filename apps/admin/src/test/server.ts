import { setupServer } from 'msw/node';
import { createOpenApiHttp } from 'openapi-msw';
import type { paths } from '../api/gen/admin';

// Моки типизированы из того же контракта, что и сервер. Общих моков нет: тест ставит свои через
// server.use(http.get('/v1/staff/me', …)); неописанный запрос — ошибка (src/test/setup.ts).
export const TEST_API = 'http://admin.test';
export const http = createOpenApiHttp<paths>({ baseUrl: TEST_API });
export const server = setupServer();
