import { useSyncExternalStore } from 'react';

// Есть ли сеть — navigator.onLine и события online/offline окна. Это подсказка браузера, а не
// проверка связи с admin-api (её делает GET /v1/staff/me): «онлайн» не гарантирует ответ сервера,
// но «офлайн» — точно не отправит. Без navigator (не браузер) — онлайн.
function subscribe(onChange: () => void): () => void {
  window.addEventListener('online', onChange);
  window.addEventListener('offline', onChange);
  return () => {
    window.removeEventListener('online', onChange);
    window.removeEventListener('offline', onChange);
  };
}

const snapshot = () => (typeof navigator === 'undefined' ? true : navigator.onLine);

export function useOnline(): boolean {
  return useSyncExternalStore(subscribe, snapshot, () => true);
}
