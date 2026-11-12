import { useCallback, useEffect, useRef } from 'react';

export type AttentionState = 'loading' | 'error' | 'ready';

// Состояние плитки из запроса: первый ответ ещё не пришёл — загрузка; ошибка без данных — ошибка; есть данные —
// готово (фоновая ошибка перечитывания данных не прячет — как DataState).
export function attentionState(q: { data: unknown; isError: boolean }): AttentionState {
  return q.data !== undefined ? 'ready' : q.isError ? 'error' : 'loading';
}

// Фокус после удачного «Повторить» (ревью UF4-H, m1): кнопка повтора исчезает вместе с ошибкой, и фокус упал бы на
// body. mark() — нажали «Повторить»; когда settled (ошибки больше нет) и фокус потерян (body), он встаёт на первую
// ссылку внутри target или на сам target. Повтор снова упал — кнопка на месте (loading держит фокус), ждём дальше;
// фокус уже ушёл куда-то сам (сотрудник щёлкнул в другом месте) — не трогаем.
export function useRetryFocus<T extends HTMLElement>(settled: boolean) {
  const target = useRef<T>(null);
  const pending = useRef(false);
  useEffect(() => {
    if (!settled || !pending.current) return;
    pending.current = false;
    const active = document.activeElement;
    if (active !== null && active !== document.body) return;
    const el = target.current;
    (el?.querySelector<HTMLElement>('a[href]') ?? el)?.focus();
  }, [settled]);
  const mark = useCallback(() => {
    pending.current = true;
  }, []);
  return { targetRef: target, mark };
}
