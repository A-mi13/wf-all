import type { createOpenApiHttp } from 'openapi-msw';
import type { paths } from '../api/gen/admin';

type Http = ReturnType<typeof createOpenApiHttp<paths>>;

// Общие ответы, нужные на любом разделе (план UF1a.1, m2): ими начинаются оба сервера моков —
// тестовый (src/test/server.ts, msw/node) и браузерный режима shots (src/shots/server.ts). Счётчик
// «Подтверждений» в каркасе (Task 15) опрашивает GET /v1/approvals на любом разделе — по умолчанию
// входящих нет. Тест подтверждений ставит свой (server.use важнее).
export function defaultHandlers(http: Http) {
  return [
    http.get('/v1/approvals', ({ response }) =>
      response(200).json({ items: [], next_cursor: null }),
    ),
  ];
}
