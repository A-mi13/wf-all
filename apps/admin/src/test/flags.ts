import type { components } from '../api/gen/admin';
import { problem } from './problem';
import { http, server } from './server';

type FeatureFlag = components['schemas']['FeatureFlag'];
type FeatureFlagPatch = components['schemas']['FeatureFlagPatch'];

export type FlagCall = { flag: string; key: string | null; body: FeatureFlagPatch };
// Свой ответ на очередной PATCH (обрыв, 409, 422, ожидание у ворот); undefined — как сервер.
export type FlagAnswer = (
  body: FeatureFlagPatch,
) => Response | undefined | Promise<Response | undefined>;

// Флаги функций как на сервере (G9-F, Task 22): GET — состояние; PATCH — сверка version (иначе 409
// platform.flag_stale), список городов целиком (повторы схлопываются, по возрастанию id), > 1000 — 400, version + 1.
// calls — что ушло (ключ идемпотентности, тело); answers — очередь своих ответов по порядку.
export function flagsServer(start: FeatureFlag[]) {
  const state = { flags: start.map((f) => ({ ...f })), reads: 0 };
  const calls: FlagCall[] = [];
  const answers: FlagAnswer[] = [];
  server.use(
    http.get('/v1/flags', ({ response }) => {
      state.reads++;
      return response(200).json({ items: state.flags });
    }),
    http.patch('/v1/flags/{key}', async ({ params, request, response }) => {
      const body = (await request.json()) as FeatureFlagPatch;
      calls.push({ flag: params.key, key: request.headers.get('Idempotency-Key'), body });
      const own = answers.shift();
      if (own) {
        const r = await own(body);
        if (r) return response.untyped(r);
      }
      const cur = state.flags.find((f) => f.key === params.key);
      if (!cur) return response.untyped(problem(404, 'platform.flag_not_found'));
      if (cur.version !== body.version)
        return response.untyped(problem(409, 'platform.flag_stale'));
      const ids = body.enabled_city_ids;
      if (ids && new Set(ids).size > 1000)
        return response.untyped(
          problem(400, 'validation.failed', {
            errors: [{ field: 'body.enabled_city_ids', code: 'max_items' }],
          }),
        );
      const next: FeatureFlag = {
        ...cur,
        enabled_globally: body.enabled_globally ?? cur.enabled_globally,
        enabled_city_ids: ids ? [...new Set(ids)].sort() : cur.enabled_city_ids,
        version: cur.version + 1,
      };
      state.flags = state.flags.map((f) => (f.key === next.key ? next : f));
      return response(200).json(next);
    }),
  );
  return { state, calls, answers };
}
