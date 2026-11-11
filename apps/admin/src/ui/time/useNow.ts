import {
  createContext,
  createElement,
  type ReactNode,
  useContext,
  useSyncExternalStore,
} from 'react';

// «Сейчас» для относительного времени (спека UI-фундамента §4.7, план UF2a.9): одна подписка на гранулу на всё
// приложение — один таймер, сколько бы «5 мин назад» ни было на экране; тик — по границе гранулы; в скрытой
// вкладке таймера нет, при возврате — сразу текущее время и снова тикает. NowProvider фиксирует время
// (истории витрины — SHOWCASE_NOW, тесты): подписки и таймера тогда нет.

export type NowGranularity = 'minute' | 'second';

const STEP: Record<NowGranularity, number> = { minute: 60_000, second: 1000 };

type Store = {
  now: Date;
  listeners: Set<() => void>;
  timer: ReturnType<typeof setTimeout> | undefined;
};

function createStore(granularity: NowGranularity) {
  const step = STEP[granularity];
  const store: Store = { now: new Date(), listeners: new Set(), timer: undefined };
  // снимок из другой гранулы (время ушло или его перевели — фейковые таймеры) — устарел
  const stale = () => Math.floor(Date.now() / step) !== Math.floor(store.now.getTime() / step);
  const hidden = () => typeof document !== 'undefined' && document.visibilityState === 'hidden';

  const refresh = () => {
    store.now = new Date();
    for (const l of store.listeners) l();
  };
  const stop = () => {
    clearTimeout(store.timer);
    store.timer = undefined;
  };
  const schedule = () => {
    stop();
    if (hidden() || store.listeners.size === 0) return;
    store.timer = setTimeout(
      () => {
        store.timer = undefined;
        refresh();
        schedule();
      },
      step - (Date.now() % step),
    );
  };
  const onVisibility = () => {
    if (hidden()) return stop();
    refresh();
    schedule();
  };

  return {
    subscribe(listener: () => void) {
      store.listeners.add(listener);
      if (store.listeners.size === 1) {
        document.addEventListener('visibilitychange', onVisibility);
        // время могло уйти, пока подписчиков не было
        if (stale()) refresh();
        schedule();
      }
      return () => {
        store.listeners.delete(listener);
        if (store.listeners.size > 0) return;
        document.removeEventListener('visibilitychange', onVisibility);
        stop();
      };
    },
    // Снимок без подписчиков может устареть — свежий берётся по границе гранулы (стабилен между вызовами).
    get(): Date {
      if (store.listeners.size === 0 && stale()) store.now = new Date();
      return store.now;
    },
  };
}

const STORES = { minute: createStore('minute'), second: createStore('second') };

const FixedNow = createContext<Date | null>(null);

export function NowProvider({ now, children }: { now: Date; children: ReactNode }) {
  return createElement(FixedNow.Provider, { value: now }, children);
}

const noSubscribe = () => () => undefined;

export function useNow(granularity: NowGranularity = 'minute'): Date {
  const fixed = useContext(FixedNow);
  const store = STORES[granularity];
  const live = useSyncExternalStore(
    fixed ? noSubscribe : store.subscribe,
    fixed ? () => fixed : store.get,
    fixed ? () => fixed : store.get,
  );
  return fixed ?? live;
}
