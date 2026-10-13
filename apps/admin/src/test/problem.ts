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
