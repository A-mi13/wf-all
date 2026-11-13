import { tokens } from '@wf/tokens';
import { useSyncExternalStore } from 'react';

// Ширина окна и устройство (спека UI-фундамента §3.2). Ширина — только раскладка (таббар, полоса,
// карточки, шторки); устройство (палец и экран уже compact) — только «только с компьютера»: компьютер
// при масштабе 200 % (640 CSS px) действий не теряет (WCAG 1.4.10). Пороги — токены breakpoint (в CSS
// @media переменные не работают, поэтому — из tokens.ts; max-width — минус 0.02, как в CSS Modules).
// Без matchMedia (тесты jsdom, старый движок) — широкий компьютер.
const below = (px: number) => `(max-width: ${px - 0.02}px)`;
const MOBILE = below(tokens.breakpoint.mobile);
const COMPACT = below(tokens.breakpoint.compact);
const HANDHELD = `(pointer: coarse) and ${COMPACT}`;

const media = (query: string) =>
  typeof window.matchMedia === 'function' ? window.matchMedia(query) : null;

// Подписка и снимок — постоянные функции на запрос (новая функция на каждый рендер переподписывала бы).
function store(query: string) {
  return {
    subscribe(onChange: () => void) {
      const m = media(query);
      m?.addEventListener('change', onChange);
      return () => m?.removeEventListener('change', onChange);
    },
    snapshot: () => media(query)?.matches ?? false,
  };
}
const mobile = store(MOBILE);
const compact = store(COMPACT);
const handheld = store(HANDHELD);
const coarse = store('(pointer: coarse)');
const server = () => false;

const useMedia = (s: ReturnType<typeof store>) =>
  useSyncExternalStore(s.subscribe, s.snapshot, server);

// Уже breakpoint.mobile: таббар, карточки вместо строк, шторка вместо панели.
export function useIsMobile(): boolean {
  return useMedia(mobile);
}

// Уже breakpoint.compact: полоса иконок вместо сайдбара, столбцы priority 'low' скрыты.
export function useIsCompact(): boolean {
  return useMedia(compact);
}

// Карманное устройство — грубый указатель (палец) И экран уже breakpoint.compact: действия из реестра
// «только с компьютера» (src/app/desktopOnly.ts) заменены пометкой. Планшет на 1024+ — рабочее место.
export function useIsHandheld(): boolean {
  return useMedia(handheld);
}

// Грубый указатель (палец) на любой ширине: плотность таблиц — всегда normal (спека §4.5).
export function useIsCoarse(): boolean {
  return useMedia(coarse);
}
