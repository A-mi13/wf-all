import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { problem } from '../test/problem';
import { providers } from '../test/render';
import { http, server } from '../test/server';
import { meMfaPending, meOwner, meSetup } from '../test/me';
import {
  browserLocale,
  DEVICE_LOCALE_KEY,
  readDeviceLocale,
  setDeviceLocale,
  useDeviceLocale,
} from './deviceLocale';
import { useLocale } from './locale';

// Язык до входа (решение пользователя 10.11; спека UI-фундамента §4.12, спека сотрудников §9.1 «Локали»):
// на стадиях без учётки (anonymous, mfa_pending, загрузка, ошибка) — выбор устройства localStorage['wf.locale'],
// пусто или мусор — язык браузера (en* → en, иначе ru); на setup и active главнее me.locale. Выход и смена
// сотрудника wf.locale не стирают; на сервер выбор устройства не уходит.

afterEach(() => {
  vi.restoreAllMocks();
  localStorage.clear();
  document.documentElement.lang = '';
});

const stubLanguage = (language: string) =>
  vi.spyOn(navigator, 'language', 'get').mockReturnValue(language);

describe('язык устройства', () => {
  test('язык браузера: en* → en, иначе ru', () => {
    expect(browserLocale('en-US')).toBe('en');
    expect(browserLocale('EN')).toBe('en');
    expect(browserLocale('ru-RU')).toBe('ru');
    expect(browserLocale('de')).toBe('ru');
    expect(browserLocale('')).toBe('ru');
    expect(browserLocale(undefined)).toBe('ru');
  });

  test('выбор в storage важнее языка браузера; пусто и мусор — язык браузера', () => {
    stubLanguage('en-GB');
    expect(readDeviceLocale()).toBe('en');
    localStorage.setItem(DEVICE_LOCALE_KEY, 'ru');
    expect(readDeviceLocale()).toBe('ru');
    for (const junk of ['', 'RU', 'fr', '"en"']) {
      localStorage.setItem(DEVICE_LOCALE_KEY, junk);
      expect(readDeviceLocale(), junk).toBe('en');
    }
  });

  test('storage бросает — язык браузера, запись без ошибки', () => {
    stubLanguage('en');
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new DOMException('denied', 'SecurityError');
    });
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new DOMException('full', 'QuotaExceededError');
    });
    expect(readDeviceLocale()).toBe('en');
    expect(() => setDeviceLocale('ru')).not.toThrow();
    // выбор не записан, но действует до перезагрузки (память вкладки); удачная запись память снимает
    const { result } = renderHook(() => useDeviceLocale());
    expect(result.current).toBe('ru');
    vi.restoreAllMocks();
    act(() => setDeviceLocale('en'));
    expect(result.current).toBe('en');
    localStorage.clear();
    stubLanguage('ru');
    act(() => window.dispatchEvent(new StorageEvent('storage', { key: null })));
    expect(result.current).toBe('ru');
  });
});

describe('useLocale по стадиям', () => {
  test('anonymous: выбор устройства, смена — сразу, <html lang> следует', async () => {
    server.use(
      http.get('/v1/staff/me', ({ response }) =>
        response.untyped(problem(401, 'auth.unauthenticated')),
      ),
    );
    localStorage.setItem(DEVICE_LOCALE_KEY, 'en');
    const { result } = renderHook(() => useLocale(), { wrapper: providers({ me: null }) });
    expect(result.current).toBe('en');
    await waitFor(() => expect(document.documentElement.lang).toBe('en'));
    act(() => setDeviceLocale('ru'));
    expect(result.current).toBe('ru');
    expect(localStorage.getItem(DEVICE_LOCALE_KEY)).toBe('ru');
    await waitFor(() => expect(document.documentElement.lang).toBe('ru'));
  });

  test('mfa_pending: выбор устройства', () => {
    localStorage.setItem(DEVICE_LOCALE_KEY, 'en');
    const { result } = renderHook(() => useLocale(), { wrapper: providers({ me: meMfaPending }) });
    expect(result.current).toBe('en');
  });

  test('setup и active: главнее me.locale, выбор устройства не учитывается', () => {
    localStorage.setItem(DEVICE_LOCALE_KEY, 'en');
    expect(
      renderHook(() => useLocale(), { wrapper: providers({ me: meSetup }) }).result.current,
    ).toBe('ru');
    expect(
      renderHook(() => useLocale(), { wrapper: providers({ me: { ...meOwner, locale: 'ru' } }) })
        .result.current,
    ).toBe('ru');
    localStorage.setItem(DEVICE_LOCALE_KEY, 'ru');
    expect(
      renderHook(() => useLocale(), { wrapper: providers({ me: { ...meOwner, locale: 'en' } }) })
        .result.current,
    ).toBe('en');
  });

  test('другая вкладка сменила язык устройства (событие storage) — применяется', () => {
    const { result } = renderHook(() => useLocale(), { wrapper: providers({ me: meMfaPending }) });
    act(() => {
      localStorage.setItem(DEVICE_LOCALE_KEY, 'en');
      window.dispatchEvent(new StorageEvent('storage', { key: DEVICE_LOCALE_KEY, newValue: 'en' }));
    });
    expect(result.current).toBe('en');
  });
});
