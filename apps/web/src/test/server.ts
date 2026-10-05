import { setupServer } from 'msw/node';
import { createOpenApiHttp } from 'openapi-msw';
import type { paths } from '../api/gen/public';

export const TEST_API = 'http://web.test';
export const http = createOpenApiHttp<paths>({ baseUrl: TEST_API });
// Ответы по умолчанию; тесты переопределяют их через server.use
export const server = setupServer(
  http.get('/v1/cities', ({ response }) =>
    response(200).json({
      items: [
        {
          id: '0199a8f0-5c3e-7a10-8b2d-3f4e5a6b7c81',
          slug: 'stavropol',
          name: 'Ставрополь',
          status: 'pilot',
          timezone: 'Europe/Moscow',
          location: { lat: 45.03442, lon: 41.9642 },
          region: { id: '0199a8f0-5c3e-7a10-8b2d-3f4e5a6b7c80', name: 'Ставропольский край' },
          country: { code: 'RU', name: 'Россия' },
        },
      ],
      next_cursor: null,
    }),
  ),
);
