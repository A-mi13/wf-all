import { useCallback, useState } from 'react';

// Черновик формы в sessionStorage по ключу маршрута (спека §9.3, P19): сессия истекла (401
// auth.unauthenticated) — вход заново, и форма восстанавливается. sessionStorage живёт, пока
// открыта вкладка, и в другие вкладки не утекает. Пароли и коды в черновик не кладутся.
const PREFIX = 'wf-admin:draft:';

function read<T>(key: string): T | undefined {
  try {
    const raw = sessionStorage.getItem(PREFIX + key);
    return raw === null ? undefined : (JSON.parse(raw) as T);
  } catch {
    return undefined; // битый JSON или хранилище недоступно — черновика нет
  }
}

function write(key: string, value: unknown) {
  try {
    if (value === undefined) sessionStorage.removeItem(PREFIX + key);
    else sessionStorage.setItem(PREFIX + key, JSON.stringify(value));
  } catch {
    // переполнение или запрет хранилища: черновик — удобство, форма работает и без него
  }
}

export function useDraft<T>(key: string): [T | undefined, (v: T) => void, () => void] {
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
