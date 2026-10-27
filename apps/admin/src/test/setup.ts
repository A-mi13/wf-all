import '@testing-library/jest-dom/vitest';
import { cleanup } from '@testing-library/react';
import { afterAll, afterEach, beforeAll } from 'vitest';
import { server } from './server';

// Неописанный запрос роняет тест (повторное ревью 27.10, Minor 5): msw 3 при onUnhandledFrame 'error'
// только пишет в консоль и обрывает fetch — экран, проглотивший обрыв, остался бы зелёным. Запросы
// копятся и проверяются после теста.
const unhandled: string[] = [];
server.events.on('request:unhandled', ({ request }) => {
  unhandled.push(`${request.method} ${request.url}`);
});

beforeAll(() => server.listen({ onUnhandledFrame: 'error' }));
afterEach(() => {
  cleanup();
  server.resetHandlers();
  const sent = unhandled.splice(0);
  if (sent.length > 0) throw new Error(`MSW: неописанные запросы — ${sent.join('; ')}`);
});
afterAll(() => server.close());
