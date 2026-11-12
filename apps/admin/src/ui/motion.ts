import { useSyncExternalStore } from 'react';

// «Уменьшить движение» системы (спека UI-фундамента §2.7, §4.15). Длительности на токенах гасит сам генератор
// (@media в tokens.css), View Transitions — global.css; JS спрашивает этот хук: сейчас — навигация с
// viewTransition (при уменьшенном движении — без него). Без matchMedia (старый движок, jsdom) — не уменьшено.
const QUERY = '(prefers-reduced-motion: reduce)';

const media = () => (typeof window.matchMedia === 'function' ? window.matchMedia(QUERY) : null);

function subscribe(onChange: () => void) {
  const m = media();
  m?.addEventListener('change', onChange);
  return () => m?.removeEventListener('change', onChange);
}
const snapshot = () => media()?.matches ?? false;
const server = () => false;

export function useReducedMotion(): boolean {
  return useSyncExternalStore(subscribe, snapshot, server);
}
