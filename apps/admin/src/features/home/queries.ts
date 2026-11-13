import { useQuery } from '@tanstack/react-query';
import { background, unwrap } from '../../api/client';
import { useApi } from '../../api/context';
import type { components } from '../../api/gen/admin';
import { APPROVALS_KEY } from '../approvals/queries';
import { CITIES_KEY } from '../geo/queries';
import { MY_MFA_KEY } from '../me/queries';

type GeoImport = components['schemas']['GeoImport'];

// Чтения главной (спека UI-фундамента §4.13): только существующие ручки, контракт не меняется. Все — фоновые
// (X-WF-Background: 1: открытая главная не продлевает сессию), без интервала — при открытии и возврате во вкладку
// (база не будится зря). enabled — право плитки: без права запроса нет. Входящие — useIncomingApprovals (ключ
// счётчика каркаса, features/approvals/queries.ts).

// Страница — максимум контракта: больше 100 строк не придёт, «99+» — по ≥ 100 строк или next_cursor.
const LIMIT = 100;
// Импорт: последние записи журнала (новые сверху). Точный «последний по каждой стране» — после разморозки контракта
// (S4): подпись плитки обещает только «по последним 5 запускам».
export const IMPORTS_LIMIT = 5;
// Импорт «завис»: идёт дольше 30 минут.
export const IMPORT_STUCK_MS = 30 * 60_000;
// «Осталось N резервных кодов» — при N ≤ 3.
export const CODES_WARN = 3;

// Свои ожидающие — под APPROVALS_KEY: решение или 202 в любом разделе перечитывает и их.
export const HOME_MINE_KEY = [...APPROVALS_KEY, 'home', 'mine'] as const;
export const HOME_IMPORTS_KEY = ['home', 'geo', 'imports'] as const;
// Города в ожидании — под CITIES_KEY: активация и правки города (invalidateCityLists) перечитывают и плитку.
export const HOME_WAITLIST_KEY = [...CITIES_KEY, 'home', 'waitlist'] as const;

const opts = { refetchOnWindowFocus: true } as const;

export function useMyPendingApprovals() {
  const api = useApi();
  return useQuery({
    ...opts,
    queryKey: HOME_MINE_KEY,
    queryFn: () =>
      unwrap(
        api.GET('/v1/approvals', {
          params: { query: { box: 'mine', status: 'pending', limit: LIMIT } },
          ...background(),
        }),
      ),
  });
}

export function useRecentImports(enabled: boolean) {
  const api = useApi();
  return useQuery({
    ...opts,
    queryKey: HOME_IMPORTS_KEY,
    queryFn: () =>
      unwrap(
        api.GET('/v1/geo/imports', {
          params: { query: { limit: IMPORTS_LIMIT } },
          ...background(),
        }),
      ),
    enabled,
  });
}

// Города в ожидании (модератору сервер сужает до своих).
export function useWaitlistCities(enabled: boolean) {
  const api = useApi();
  return useQuery({
    ...opts,
    queryKey: HOME_WAITLIST_KEY,
    queryFn: () =>
      unwrap(
        api.GET('/v1/geo/cities', {
          params: { query: { status: ['waitlist'], limit: LIMIT } },
          ...background(),
        }),
      ),
    enabled,
  });
}

export function useMfaStatus() {
  const api = useApi();
  return useQuery({
    ...opts,
    // ключ «Моего доступа»: перевыпуск кодов (RegenerateCodesModal) обновляет и главную, старое число не мелькает
    queryKey: MY_MFA_KEY,
    queryFn: () => unwrap(api.GET('/v1/staff/me/mfa', background())),
  });
}

export type ImportAlert = { country: string; kind: 'failed' | 'stuck'; at: string };

// Среди записей (новые сверху) — последняя по каждой стране; внимание — упала или идёт дольше 30 минут. Порядок —
// как в журнале (новые сверху).
export function importAlerts(items: GeoImport[], now: number): ImportAlert[] {
  const seen = new Set<string>();
  const out: ImportAlert[] = [];
  for (const it of items) {
    if (seen.has(it.country_code)) continue;
    seen.add(it.country_code);
    if (it.status === 'failed') {
      out.push({ country: it.country_code, kind: 'failed', at: it.finished_at ?? it.started_at });
    } else if (it.status === 'running' && now - Date.parse(it.started_at) > IMPORT_STUCK_MS) {
      out.push({ country: it.country_code, kind: 'stuck', at: it.started_at });
    }
  }
  return out;
}
