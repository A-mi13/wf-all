import { expect, test } from 'vitest';
import { approvalsServer, INCOMING } from './approvals';
import { TEST_API } from './server';

// Мок «Подтверждений» (ревью уборки №7 m4): ответ-ошибка решения не по Problem (прокси, HTML) уходит
// экрану как есть — afterError не падает на теле не-JSON и состояние не меняет.
test('ошибка решения с телом не-JSON — ответ как есть, состояние прежнее', async () => {
  const srv = approvalsServer({
    answer: {
      approve: () =>
        new Response('<html>Bad Gateway</html>', {
          status: 502,
          headers: { 'Content-Type': 'text/html' },
        }),
    },
  });
  const r = await fetch(`${TEST_API}/v1/approvals/${INCOMING.id}/approve`, {
    method: 'POST',
    headers: { 'Idempotency-Key': 'c8-key', 'Content-Type': 'application/json' },
    body: '{}',
  });
  expect(r.status).toBe(502);
  expect(await r.text()).toBe('<html>Bad Gateway</html>');
  expect(srv.state.incoming).toEqual([INCOMING]);
  expect(srv.seen.decisions).toHaveLength(1);
});
