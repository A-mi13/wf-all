import createClient from 'openapi-fetch';
import type { paths } from './gen/admin';

// Клиент админского API. Существует только в apps/admin — веб его физически не видит.
export function createApi(baseUrl: string) {
  return createClient<paths>({ baseUrl });
}

export type Api = ReturnType<typeof createApi>;
