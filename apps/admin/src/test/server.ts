import { setupServer } from 'msw/node';
import { createOpenApiHttp } from 'openapi-msw';
import type { paths } from '../api/gen/admin';

// Моки типизированы из того же контракта, что и сервер.
export const TEST_API = 'http://admin.test';
export const http = createOpenApiHttp<paths>({ baseUrl: TEST_API });
export const server = setupServer(
  http.get('/v1/health', ({ response }) => response(200).json({ status: 'ok' })),
);
