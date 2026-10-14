import { HttpResponse } from 'msw';
import { describe, expect, test } from 'vitest';
import { createReauthBus } from '../app/reauth';
import { meOwner } from '../test/me';
import { problem } from '../test/problem';
import { http, server, TEST_API } from '../test/server';
import { ApiError, background, createApi, unwrap } from './client';

// ключ идемпотентности действия — его даёт useAdminMutation, клиент только передаёт
const KEY = crypto.randomUUID();
const reauth = () => problem(401, 'auth.reauth_required');

// Ошибка, которой отклонился промис.
async function caught(p: Promise<unknown>): Promise<ApiError> {
  try {
    await p;
  } catch (e) {
    return e as ApiError;
  }
  throw new Error('ожидалась ошибка');
}

// Дождаться условия, проверяя его на каждой итерации событийного цикла.
async function until(cond: () => boolean) {
  for (let i = 0; i < 200 && !cond(); i++) await new Promise((r) => setTimeout(r, 0));
  expect(cond()).toBe(true);
}

describe('X-WF-Admin', () => {
  test('только на изменяющих методах', async () => {
    const seen: Record<string, string | null> = {};
    server.use(
      http.get('/v1/staff/me', ({ request, response }) => {
        seen.get = request.headers.get('X-WF-Admin');
        return response(200).json(meOwner);
      }),
      http.patch('/v1/staff/me/locale', ({ request, response }) => {
        seen.patch = request.headers.get('X-WF-Admin');
        return response(204).empty();
      }),
      http.delete('/v1/staff/me/sessions/{sessionId}', ({ request, response }) => {
        seen.delete = request.headers.get('X-WF-Admin');
        return response(204).empty();
      }),
    );
    const api = createApi(TEST_API);
    await api.GET('/v1/staff/me');
    await api.PATCH('/v1/staff/me/locale', {
      params: { header: { 'Idempotency-Key': KEY } },
      body: { locale: 'en' },
    });
    await api.DELETE('/v1/staff/me/sessions/{sessionId}', {
      params: { header: { 'Idempotency-Key': KEY }, path: { sessionId: meOwner.session!.id } },
    });
    expect(seen).toEqual({ get: null, patch: '1', delete: '1' });
  });

  test('ключ идемпотентности клиент сам не создаёт (M6)', async () => {
    let key: string | null = 'не вызван';
    server.use(
      http.post('/v1/staff/auth/login', ({ request, response }) => {
        key = request.headers.get('Idempotency-Key');
        return response(200).json({ stage: 'mfa_pending' });
      }),
    );
    await createApi(TEST_API).POST('/v1/staff/auth/login', {
      body: { email: 'olga.v@wf.example', password: 'Жёлтая карточка на 90-й' },
    });
    expect(key).toBeNull();
  });
});

describe('Problem → ApiError', () => {
  test('validation.failed — ошибки по полям', async () => {
    server.use(
      http.post('/v1/staff/auth/invite/accept', ({ response }) =>
        response.untyped(
          problem(400, 'validation.failed', {
            errors: [{ field: 'body.password', code: 'too_short' }],
          }),
        ),
      ),
    );
    const err = await caught(
      createApi(TEST_API).POST('/v1/staff/auth/invite/accept', {
        body: { email: 'olga.v@wf.example', code: 'k7m2p-x4q6d', password: 'коротко' },
      }),
    );
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({
      status: 400,
      code: 'validation.failed',
      fields: [{ field: 'body.password', code: 'too_short' }],
    });
    expect(err.retryAfter).toBeUndefined();
    expect(err.attemptsLeft).toBeUndefined();
  });

  test('429 — retryAfter из тела', async () => {
    server.use(
      http.post('/v1/staff/auth/login', ({ response }) =>
        response.untyped(
          problem(429, 'ratelimit.exceeded', { retry_after: 42 }, { 'Retry-After': '42' }),
        ),
      ),
    );
    const err = await caught(
      createApi(TEST_API).POST('/v1/staff/auth/login', {
        body: { email: 'olga.v@wf.example', password: 'x' },
      }),
    );
    expect(err).toMatchObject({ status: 429, code: 'ratelimit.exceeded', retryAfter: 42 });
  });

  test('429 — retryAfter из заголовка, если в теле нет', async () => {
    server.use(
      http.post('/v1/staff/auth/login', ({ response }) =>
        response.untyped(problem(429, 'ratelimit.exceeded', {}, { 'Retry-After': '30' })),
      ),
    );
    const err = await caught(
      createApi(TEST_API).POST('/v1/staff/auth/login', {
        body: { email: 'olga.v@wf.example', password: 'x' },
      }),
    );
    expect(err.retryAfter).toBe(30);
  });

  test('staff.mfa_invalid — attemptsLeft, и 0 тоже', async () => {
    server.use(
      http.post('/v1/staff/auth/mfa', ({ response }) =>
        response.untyped(problem(400, 'staff.mfa_invalid', { attempts_left: 0 })),
      ),
    );
    const err = await caught(
      createApi(TEST_API).POST('/v1/staff/auth/mfa', {
        params: { header: { 'Idempotency-Key': KEY } },
        body: { code: '492817' },
      }),
    );
    expect(err).toMatchObject({ status: 400, code: 'staff.mfa_invalid', attemptsLeft: 0 });
  });

  test('ответ не Problem (прокси отдал HTML) — код internal со статусом ответа', async () => {
    server.use(
      http.get('/v1/staff/me', ({ response }) =>
        response.untyped(new HttpResponse('<html>Bad Gateway</html>', { status: 502 })),
      ),
    );
    const err = await caught(createApi(TEST_API).GET('/v1/staff/me'));
    expect(err).toMatchObject({ status: 502, code: 'internal' });
  });
});

describe('reauth_required → разблокировка и повтор (P20)', () => {
  const body = {
    current_password: 'Жёлтая карточка на 90-й',
    code: '492817',
    new_password: 'Красная карточка на 91-й',
  };

  // Два параллельных запроса: GET и POST с телом. До разблокировки оба получают 401.
  function setup() {
    const state = { unlocked: false, hits: 0, bodies: [] as unknown[] };
    server.use(
      http.get('/v1/staff/me/sessions', ({ response }) => {
        state.hits++;
        return state.unlocked ? response(200).json({ items: [] }) : response.untyped(reauth());
      }),
      http.post('/v1/staff/me/password', async ({ request, response }) => {
        state.hits++;
        state.bodies.push(await request.json());
        return state.unlocked ? response(204).empty() : response.untyped(reauth());
      }),
    );
    const bus = createReauthBus();
    let opened = 0;
    bus.subscribe(() => {
      if (bus.pending()) opened++;
    });
    const api = createApi(TEST_API, { reauth: bus });
    const run = () =>
      Promise.all([
        api.GET('/v1/staff/me/sessions'),
        api.POST('/v1/staff/me/password', {
          params: { header: { 'Idempotency-Key': KEY } },
          body,
        }),
      ]);
    return { state, bus, api, run, opened: () => opened };
  }

  test('одна разблокировка на два запроса, после resolve оба повторяются; тело POST — целиком', async () => {
    const { state, bus, run, opened } = setup();
    const done = run();
    await until(() => state.hits === 2 && bus.pending());
    expect(opened()).toBe(1);

    state.unlocked = true;
    bus.resolve();
    const [list, change] = await done;

    expect(list.data).toEqual({ items: [] });
    expect(change.response.status).toBe(204);
    expect(state.hits).toBe(4);
    expect(state.bodies).toEqual([body, body]);
    expect(opened()).toBe(1);
  });

  test('после reject оба падают с auth.reauth_required', async () => {
    const { state, bus, api } = setup();
    const done = Promise.allSettled([
      api.GET('/v1/staff/me/sessions'),
      api.POST('/v1/staff/me/password', {
        params: { header: { 'Idempotency-Key': KEY } },
        body,
      }),
    ]);
    await until(() => state.hits === 2 && bus.pending());
    bus.reject();
    const results = await done;
    expect(results.map((r) => r.status)).toEqual(['rejected', 'rejected']);
    for (const r of results) {
      const err = r.status === 'rejected' ? r.reason : undefined;
      expect(err).toBeInstanceOf(ApiError);
      expect(err).toMatchObject({ status: 401, code: 'auth.reauth_required' });
    }
    expect(state.hits).toBe(2);
  });

  test('повтор снова получил reauth_required — ошибка, без второго круга', async () => {
    const { state, bus, run } = setup();
    const done = run();
    await until(() => state.hits === 2 && bus.pending());
    bus.resolve();
    const err = await caught(done);
    expect(err).toMatchObject({ status: 401, code: 'auth.reauth_required' });
    expect(state.hits).toBe(4);
    expect(bus.pending()).toBe(false);
  });
});

describe('background() — фоновые опросы (Review Focus 2)', () => {
  test('ставит X-WF-Background: 1 и сохраняет свои заголовки', async () => {
    let seen: [string | null, string | null] = [null, null];
    server.use(
      http.get('/v1/approvals', ({ request, response }) => {
        seen = [request.headers.get('X-WF-Background'), request.headers.get('X-Trace')];
        return response(200).json({ items: [], next_cursor: null });
      }),
    );
    await createApi(TEST_API).GET('/v1/approvals', {
      params: { query: { box: 'incoming', status: 'pending' } },
      ...background({ 'X-Trace': 't1' }),
    });
    expect(seen).toEqual(['1', 't1']);
    expect(background()).toEqual({ headers: { 'X-WF-Background': '1' } });
  });

  test('обычный запрос заголовка не несёт', async () => {
    let seen: string | null = 'не вызван';
    server.use(
      http.get('/v1/approvals', ({ request, response }) => {
        seen = request.headers.get('X-WF-Background');
        return response(200).json({ items: [], next_cursor: null });
      }),
    );
    await createApi(TEST_API).GET('/v1/approvals', {
      params: { query: { box: 'incoming', status: 'pending' } },
    });
    expect(seen).toBeNull();
  });

  // Спека §9.3: вкладка с опросом через 20 минут бездействия показывает модалку разблокировки,
  // а не тихо замёрзший счётчик.
  test('фоновый reauth_required открывает разблокировку; после resolve опрос повторяется с меткой', async () => {
    let unlocked = false;
    const marks: (string | null)[] = [];
    server.use(
      http.get('/v1/approvals', ({ request, response }) => {
        marks.push(request.headers.get('X-WF-Background'));
        return unlocked
          ? response(200).json({ items: [], next_cursor: null })
          : response.untyped(reauth());
      }),
    );
    const bus = createReauthBus();
    let opened = 0;
    bus.subscribe(() => {
      if (bus.pending()) opened++;
    });
    const done = createApi(TEST_API, { reauth: bus }).GET('/v1/approvals', {
      params: { query: { box: 'incoming', status: 'pending' } },
      ...background(),
    });
    await until(() => bus.pending());
    expect(opened).toBe(1);

    unlocked = true;
    bus.resolve();
    const res = await done;
    expect(res.data).toEqual({ items: [], next_cursor: null });
    expect(marks).toEqual(['1', '1']);
    expect(opened).toBe(1);
  });

  test('при открытой разблокировке фоновый запрос ждёт её, не открывая повторно, и повторяется', async () => {
    let unlocked = false;
    let hits = 0;
    server.use(
      http.get('/v1/approvals', ({ response }) => {
        hits++;
        return unlocked
          ? response(200).json({ items: [], next_cursor: null })
          : response.untyped(reauth());
      }),
    );
    const bus = createReauthBus();
    void bus.request(); // модалка уже открыта (например, после перезагрузки с reauth_required)
    let notified = 0;
    bus.subscribe(() => notified++);

    const done = createApi(TEST_API, { reauth: bus }).GET('/v1/approvals', {
      params: { query: { box: 'incoming', status: 'pending' } },
      ...background(),
    });
    await until(() => hits === 1);
    expect(notified).toBe(0);

    unlocked = true;
    bus.resolve();
    const res = await done;
    expect(res.data).toEqual({ items: [], next_cursor: null });
    expect(hits).toBe(2);
    expect(notified).toBe(1); // только закрытие
  });
});

test('запросы — с credentials same-origin', async () => {
  let creds: RequestCredentials | undefined;
  server.use(
    http.get('/v1/staff/me', ({ request, response }) => {
      creds = request.credentials;
      return response(200).json(meOwner);
    }),
  );
  await createApi(TEST_API).GET('/v1/staff/me');
  expect(creds).toBe('same-origin');
});

describe('unwrap — данные ответа без ! и as', () => {
  test('2xx с телом — данные', async () => {
    server.use(http.get('/v1/staff/me', ({ response }) => response(200).json(meOwner)));
    const me = await unwrap(createApi(TEST_API).GET('/v1/staff/me'));
    expect(me.email).toBe(meOwner.email);
  });

  test('не-2xx — ApiError клиента пробрасывается как есть', async () => {
    server.use(
      http.get('/v1/staff/me', ({ response }) =>
        response.untyped(problem(401, 'auth.unauthenticated')),
      ),
    );
    const e = await caught(unwrap(createApi(TEST_API).GET('/v1/staff/me')));
    expect(e).toBeInstanceOf(ApiError);
    expect(e.code).toBe('auth.unauthenticated');
  });

  test('2xx без тела там, где его ждут, — ApiError internal со статусом ответа', async () => {
    server.use(http.get('/v1/staff/me', () => new HttpResponse(null, { status: 204 })));
    const e = await caught(unwrap(createApi(TEST_API).GET('/v1/staff/me')));
    expect(e).toBeInstanceOf(ApiError);
    expect(e).toMatchObject({ status: 204, code: 'internal' });
  });
});
