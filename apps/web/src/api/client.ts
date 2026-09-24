import createClient from 'openapi-fetch';
import type { paths } from './gen/public';

// Клиент публичного API.
export function createApi(baseUrl: string) {
  return createClient<paths>({ baseUrl });
}

export type Api = ReturnType<typeof createApi>;
