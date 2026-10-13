import { tokens } from '@wf/tokens';
import { useSyncExternalStore } from 'react';

// Узкий экран — уже breakpoint-mobile (токен; в CSS @media переменные не работают, поэтому
// ширина — из tokens.ts). Без matchMedia (тесты jsdom) — широкий.
const QUERY = `(max-width: ${tokens.breakpoint.mobile - 0.02}px)`;

const media = () => (typeof window.matchMedia === 'function' ? window.matchMedia(QUERY) : null);

function subscribe(onChange: () => void): () => void {
  const m = media();
  m?.addEventListener('change', onChange);
  return () => m?.removeEventListener('change', onChange);
}

export function useIsMobile(): boolean {
  return useSyncExternalStore(
    subscribe,
    () => media()?.matches ?? false,
    () => false,
  );
}
