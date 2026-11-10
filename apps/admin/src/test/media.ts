import { onTestFinished } from 'vitest';

// Устройство для теста (спека UI-фундамента §3.2): matchMedia, который разбирает запросы ширины
// (max-width / min-width, в px) и указателя (pointer: fine | coarse), в том числе составные через
// «and». Ширина решает только раскладку, устройство (палец и экран уже compact) — «только с
// компьютера»: стаб отвечает на каждый признак отдельно, а не «да» на любой запрос. Системная тема
// (prefers-color-scheme) — светлая. Незнакомый признак — ошибка: тест не должен молча получить «нет».
// set() меняет устройство на лету и оповещает подписчиков (change), как браузер. После теста
// matchMedia возвращается прежний.
export type MediaDevice = { width: number; pointer: 'fine' | 'coarse' };

type Listener = (e: { matches: boolean; media: string }) => void;

function feature(part: string, device: MediaDevice): boolean {
  const m = /^\(\s*([a-z-]+)\s*:\s*([^)]+?)\s*\)$/.exec(part.trim());
  if (!m) throw new Error(`stubMedia: незнакомый запрос «${part}»`);
  const [, name, value] = m;
  const px = () => {
    const n = /^(\d+(?:\.\d+)?)px$/.exec(value ?? '');
    if (!n) throw new Error(`stubMedia: ширина не в px — «${part}»`);
    return Number(n[1]);
  };
  switch (name) {
    case 'max-width':
      return device.width <= px();
    case 'min-width':
      return device.width >= px();
    case 'pointer':
      return value === device.pointer;
    case 'prefers-color-scheme':
      return value === 'light';
    default:
      throw new Error(`stubMedia: незнакомый признак «${part}»`);
  }
}

export function mediaMatches(query: string, device: MediaDevice): boolean {
  return query.split(/\s+and\s+/).every((part) => feature(part, device));
}

export function stubMedia(initial: MediaDevice) {
  let device = initial;
  const lists: { query: string; listeners: Set<Listener>; last: boolean }[] = [];
  const original = Object.getOwnPropertyDescriptor(window, 'matchMedia');
  const matchMedia = (query: string) => {
    mediaMatches(query, device); // незнакомый запрос — ошибка сразу
    const entry = { query, listeners: new Set<Listener>(), last: mediaMatches(query, device) };
    lists.push(entry);
    return {
      media: query,
      get matches() {
        return mediaMatches(query, device);
      },
      onchange: null,
      addEventListener: (_: 'change', fn: Listener) => entry.listeners.add(fn),
      removeEventListener: (_: 'change', fn: Listener) => entry.listeners.delete(fn),
      addListener: (fn: Listener) => entry.listeners.add(fn),
      removeListener: (fn: Listener) => entry.listeners.delete(fn),
      dispatchEvent: () => true,
    };
  };
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    writable: true,
    value: matchMedia,
  });
  onTestFinished(() => {
    if (original) Object.defineProperty(window, 'matchMedia', original);
    else Reflect.deleteProperty(window, 'matchMedia');
  });
  return {
    // Новое устройство: подписчики запросов, чей ответ изменился, получают change.
    set(next: MediaDevice) {
      device = next;
      for (const entry of lists) {
        const matches = mediaMatches(entry.query, device);
        if (matches === entry.last) continue;
        entry.last = matches;
        for (const fn of [...entry.listeners]) fn({ matches, media: entry.query });
      }
    },
  };
}
