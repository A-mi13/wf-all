import {
  type NavigateOptions,
  useNavigate,
  useRouter,
  useRouterState,
} from '@tanstack/react-router';
import { useCallback, useEffect, useRef } from 'react';
import type { PanelKey } from './paths';

// Панель в адресе (спека UI-фундамента §4.6, план UF2b.8; m9, m10, m13, R2-2). Открытая панель — search-параметр
// из PANEL_SEARCH_KEYS: F5, «Назад» и пересланная ссылка её не теряют.
// - Открытие изнутри — navigate с push и меткой state.panel; закрытие такой — router.history.back (запись панели
//   снимается). Метка живёт в history.state и переживает F5 — после F5 back по-прежнему верен.
// - Открыта глубокой ссылкой (метки нет) — закрытие navigate без ключа с replace (back увёл бы из приложения).
// - Закрытие — навигация с ignoreBlocker: true: вопрос «Уйти без сохранения?» уже задал Overlay.dirty (N4), второго нет.
//   Других исключений у блокировщика нет: браузерная «Назад» с грязной панели спрашивает useLeaveGuard.
// - close идемпотентен в пределах одного открытия (запись истории): двойной Esc до popstate — один back (m13).
// - Ключ без права (allowed: false) — панель не открывается, ключ снимается replace; так же, если права сняли, пока
//   панель открыта (уход не по воле сотрудника — без вопроса, §4.11; черновик формы, где он есть, цел).
// - Прокрутка окна не сбрасывается (resetScroll: false у open, close-replace и снятия ключа): панель поверх списка,
//   список под затемнением остаётся на месте (ревью UF2b/II-B, m1; router-core link.ts resetScroll, по умолчанию true —
//   scrollTo(0, 0) после навигации).
// - Значение проверяется по ключу (id — uuid, флаг — 1, страна — ISO alpha-2): негодное — панели нет.

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const COUNTRY = /^[A-Z]{2}$/;

// Значение ключа: parse — из search роутера (значения он разбирает как JSON: «1» приходит числом) в строку панели;
// url — обратно, в то значение, которое роутер запишет как есть (?invite=1, а не ?invite=%221%22).
type Spec = { parse(v: unknown): string | undefined; url(v: string): unknown };
const flag: Spec = {
  parse: (v) => (v === 1 || v === '1' ? '1' : undefined),
  url: () => 1,
};
const pattern = (re: RegExp): Spec => ({
  parse: (v) => (typeof v === 'string' && re.test(v) ? v : undefined),
  url: (v) => v,
});
const SPEC: Record<PanelKey, Spec> = {
  account: pattern(UUID),
  invite: flag,
  activate: flag,
  entry: pattern(UUID),
  edit: pattern(COUNTRY),
};

// validateSearch маршрута: годные значения его ключей панелей (в виде для адреса), остальное отбрасывается.
export function panelSearch<K extends PanelKey>(keys: readonly K[]) {
  return (s: Record<string, unknown>): Partial<Record<K, unknown>> => {
    const out: Partial<Record<K, unknown>> = {};
    for (const k of keys) {
      const v = SPEC[k].parse(s[k]);
      if (v !== undefined) out[k] = SPEC[k].url(v);
    }
    return out;
  };
}

const without = (search: Record<string, unknown>, key: string) => {
  const next = { ...search };
  delete next[key];
  return next;
};

const marked = (state: unknown) =>
  typeof state === 'object' && state !== null && (state as { panel?: unknown }).panel === true;

export type PanelParam = {
  value: string | undefined;
  open(value: string): void;
  close(): void;
};

export function usePanelParam<K extends PanelKey>(key: K, opts: { allowed: boolean }): PanelParam {
  const router = useRouter();
  const raw = useRouterState({
    select: (s) => (s.location.search as Record<string, unknown>)[key],
  });
  const present = raw !== undefined;
  const value = opts.allowed ? SPEC[key].parse(raw) : undefined;

  // Без права — ключ снимается (и когда права сняли при открытой панели).
  useEffect(() => {
    if (opts.allowed || !present) return;
    void router.navigate({
      to: '.',
      search: (prev: Record<string, unknown>) => without(prev, key),
      replace: true,
      ignoreBlocker: true,
      resetScroll: false,
    });
  }, [router, key, opts.allowed, present]);

  // Запись истории, для которой close уже исполнен: повтор до смены адреса — ничего (m13). Метка — на одно
  // появление записи: смена записи (popstate «Назад», «Вперёд» браузера возвращает ту же запись с тем же ключом)
  // её снимает, иначе панель, вернувшуюся «Вперёд», нельзя было бы закрыть (ревью UF2b/II-B, I1).
  const closed = useRef<string | null>(null);
  const entry = useRouterState({ select: (s) => s.location.state.__TSR_key ?? s.location.href });
  useEffect(() => {
    closed.current = null;
  }, [entry]);

  const open = useCallback(
    (v: string) => {
      void router.navigate({
        to: '.',
        search: (prev: Record<string, unknown>) => ({ ...prev, [key]: SPEC[key].url(v) }),
        state: (prev) => Object.assign({}, prev, { panel: true }),
        resetScroll: false,
      });
    },
    [router, key],
  );

  const close = useCallback(() => {
    const loc = router.state.location;
    if ((loc.search as Record<string, unknown>)[key] === undefined) return;
    const at = loc.state.__TSR_key ?? loc.href;
    if (closed.current === at) return;
    closed.current = at;
    if (marked(loc.state)) {
      router.history.back({ ignoreBlocker: true });
      return;
    }
    void router.navigate({
      to: '.',
      search: (prev: Record<string, unknown>) => without(prev, key),
      replace: true,
      ignoreBlocker: true,
      resetScroll: false,
    });
  }, [router, key]);

  return { value, open, close };
}

// Уход из панели на другой путь (успех активации → страница города, «Открыть город»): replace — запись панели
// заменяется, браузерная «Назад» ведёт на список, а не в панель.
export function useLeavePanelTo(): (to: NavigateOptions) => void {
  const navigate = useNavigate();
  return useCallback((to: NavigateOptions) => void navigate({ ...to, replace: true }), [navigate]);
}
