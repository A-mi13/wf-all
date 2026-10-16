import { setupServer } from 'msw/node';
import { createOpenApiHttp } from 'openapi-msw';
import type { paths } from '../api/gen/admin';

// Моки типизированы из того же контракта, что и сервер. Тест ставит свои через
// server.use(http.get('/v1/staff/me', …)); неописанный запрос — ошибка (src/test/setup.ts).
// Общий мок один: счётчик «Подтверждений» в каркасе (Task 15) опрашивает GET /v1/approvals на любом
// разделе — по умолчанию входящих нет. Тест подтверждений ставит свой (server.use важнее).
export const TEST_API = 'http://admin.test';
export const http = createOpenApiHttp<paths>({ baseUrl: TEST_API });
export const server = setupServer(
  http.get('/v1/approvals', ({ response }) => response(200).json({ items: [], next_cursor: null })),
);
