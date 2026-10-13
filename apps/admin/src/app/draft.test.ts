import { act, renderHook } from '@testing-library/react';
import { afterEach, expect, test } from 'vitest';
import { useDraft } from './draft';

afterEach(() => sessionStorage.clear());

type Form = { email: string; name: string };

test('черновик формы переживает размонтирование (вход заново, перезагрузка)', () => {
  const first = renderHook(() => useDraft<Form>('/staff'));
  expect(first.result.current[0]).toBeUndefined();
  act(() => first.result.current[1]({ email: 'ivan.p@wf.example', name: 'Иван П.' }));
  expect(first.result.current[0]).toEqual({ email: 'ivan.p@wf.example', name: 'Иван П.' });
  first.unmount();

  const again = renderHook(() => useDraft<Form>('/staff'));
  expect(again.result.current[0]).toEqual({ email: 'ivan.p@wf.example', name: 'Иван П.' });
});

test('ключ маршрута разделяет черновики; clear() удаляет', () => {
  const staff = renderHook(() => useDraft<Form>('/staff'));
  act(() => staff.result.current[1]({ email: 'a@wf.example', name: 'A' }));
  expect(renderHook(() => useDraft<Form>('/me')).result.current[0]).toBeUndefined();

  act(() => staff.result.current[2]());
  expect(staff.result.current[0]).toBeUndefined();
  expect(renderHook(() => useDraft<Form>('/staff')).result.current[0]).toBeUndefined();
});

test('битый JSON в хранилище — черновика нет, без исключения', () => {
  sessionStorage.setItem('wf-admin:draft:/staff', '{не json');
  expect(renderHook(() => useDraft<Form>('/staff')).result.current[0]).toBeUndefined();
});
