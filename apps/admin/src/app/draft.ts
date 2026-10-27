import { useCallback, useState } from 'react';

// Черновик формы в sessionStorage по ключу маршрута (спека §9.3, P19): сессия истекла (401
// auth.unauthenticated) — вход заново, и форма восстанавливается. sessionStorage живёт, пока
// открыта вкладка, и в другие вкладки не утекает. Пароли и коды в черновик не кладутся.
// Черновик внутри каркаса — с владельцем (owner = me.id, ⚠️ Task 14): после истечения сессии за тот
// же компьютер может сесть другой сотрудник — чужой черновик он не видит, а его вход стирает чужие
// (claimDrafts). Без владельца — только экраны входа (почта до входа).
const PREFIX = 'wf-admin:draft:';
const OWNED = `${PREFIX}@`;

const storageKey = (key: string, owner?: string) =>
  owner === undefined ? PREFIX + key : `${OWNED}${encodeURIComponent(owner)}:${key}`;

function read<T>(key: string): T | undefined {
  try {
    const raw = sessionStorage.getItem(key);
    return raw === null ? undefined : (JSON.parse(raw) as T);
  } catch {
    return undefined; // битый JSON или хранилище недоступно — черновика нет
  }
}

function write(key: string, value: unknown) {
  try {
    if (value === undefined) sessionStorage.removeItem(key);
    else sessionStorage.setItem(key, JSON.stringify(value));
  } catch {
    // переполнение или запрет хранилища: черновик — удобство, форма работает и без него
  }
}

export function useDraft<T>(
  route: string,
  owner?: string,
): [T | undefined, (v: T) => void, () => void] {
  const key = storageKey(route, owner);
  const [value, setValue] = useState<T | undefined>(() => read<T>(key));
  const save = useCallback(
    (v: T) => {
      write(key, v);
      setValue(v);
    },
    [key],
  );
  const clear = useCallback(() => {
    write(key, undefined);
    setValue(undefined);
  }, [key]);
  return [value, save, clear];
}

// Явный выход (useLogout): черновики прежнего сотрудника не достаются следующему за этим же
// компьютером. Стираются только ключи админки.
export function clearDrafts() {
  try {
    const keys = Array.from({ length: sessionStorage.length }, (_, i) => sessionStorage.key(i));
    for (const k of keys) if (k?.startsWith(PREFIX)) sessionStorage.removeItem(k);
  } catch {
    // хранилище недоступно — и черновиков в нём нет
  }
}

// Вход сотрудника owner (сессия с me.id): черновики других сотрудников из этой вкладки стираются —
// следующему за компьютером они не достаются ни в форме, ни в storage. Свои и черновики экранов входа
// (без владельца) остаются.
export function claimDrafts(owner: string) {
  const own = `${OWNED}${encodeURIComponent(owner)}:`;
  try {
    const keys = Array.from({ length: sessionStorage.length }, (_, i) => sessionStorage.key(i));
    for (const k of keys)
      if (k?.startsWith(OWNED) && !k.startsWith(own)) sessionStorage.removeItem(k);
  } catch {
    // хранилище недоступно — и черновиков в нём нет
  }
}
