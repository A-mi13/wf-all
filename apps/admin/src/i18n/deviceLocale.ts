import { useSyncExternalStore } from 'react';
import type { Locale } from './messages';

// Язык до входа (решение пользователя 10.11; спека сотрудников §9.1 «Локали», спека UI-фундамента §4.12):
// выбор на устройстве — localStorage['wf.locale'], как тема (свойство браузера, не сотрудника: выход и смена
// сотрудника его не стирают, на сервер не уходит). Пусто или мусор — язык браузера (en* → en, иначе ru).
// Действует на стадиях без учётки (anonymous, mfa_pending, загрузка, ошибка); на setup и active главнее
// me.locale (useLocale в ./locale.tsx).
export const DEVICE_LOCALE_KEY = 'wf.locale';

const parse = (raw: unknown): Locale | null => (raw === 'ru' || raw === 'en' ? raw : null);

export function browserLocale(language: string | undefined = navigator.language): Locale {
  return language?.toLowerCase().startsWith('en') ? 'en' : 'ru';
}

function stored(): Locale | null {
  try {
    return parse(localStorage.getItem(DEVICE_LOCALE_KEY));
  } catch {
    return null; // приватный режим
  }
}

export function readDeviceLocale(): Locale {
  return stored() ?? browserLocale();
}

// Выбор, который не удалось записать (приватный режим, переполнение), живёт в памяти вкладки до перезагрузки
// или до события storage — иначе переключатель ничего бы не делал. Удачная запись память снимает.
let memory: Locale | null = null;
const snapshot = (): Locale => memory ?? readDeviceLocale();

const listeners = new Set<() => void>();

export function setDeviceLocale(locale: Locale): void {
  try {
    localStorage.setItem(DEVICE_LOCALE_KEY, locale);
    memory = null;
  } catch {
    // приватный режим или переполнение: язык меняется до перезагрузки, выбор не запомнится
    memory = locale;
  }
  for (const l of listeners) l();
}

function subscribe(onChange: () => void): () => void {
  listeners.add(onChange);
  // другая вкладка сменила язык устройства (или стёрла storage — key === null)
  const onStorage = (e: StorageEvent) => {
    if (e.key !== null && e.key !== DEVICE_LOCALE_KEY) return;
    memory = null;
    onChange();
  };
  window.addEventListener('storage', onStorage);
  return () => {
    listeners.delete(onChange);
    window.removeEventListener('storage', onStorage);
  };
}

export function useDeviceLocale(): Locale {
  return useSyncExternalStore(subscribe, snapshot, () => 'ru');
}
