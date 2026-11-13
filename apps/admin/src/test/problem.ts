import { HttpResponse } from 'msw';
import type { components } from '../api/gen/admin';

type Problem = components['schemas']['Problem'];

// Ответ-ошибка по RFC 9457, как его отдаёт admin-api. Для моков openapi-msw — через
// response.untyped(problem(...)).
export function problem(
  status: number,
  code: string,
  extra: Partial<Problem> = {},
  headers: Record<string, string> = {},
): HttpResponse<Problem> {
  return HttpResponse.json<Problem>(
    { type: 'about:blank', title: 'Error', status, code, ...extra },
    { status, headers: { 'Content-Type': 'application/problem+json', ...headers } },
  );
}

// Поиск городов и мест (listGeoCities, listGeoPlaces): сервер нормализует q и на пустой после нормализации — без
// буквы и цифры («-», «..») — отвечает 400 validation.failed по query.q (geo/admin handler.go errBadQuery). Мок
// повторяет правило сервера, а не клиента: экран, отправивший такой q, увидит ошибку, как на стенде.
export function emptyQueryProblem(search: URLSearchParams): HttpResponse<Problem> | undefined {
  const q = search.get('q');
  if (q === null || /[\p{L}\p{N}]/u.test(q)) return undefined;
  return problem(400, 'validation.failed', {
    errors: [{ field: 'query.q', code: 'minLength' }],
  });
}
