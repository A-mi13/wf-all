import { diffKeys, withFallback } from '@wf/i18n';
import { expect, test } from 'vitest';
import en from '../../messages/en.json';
import ru from '../../messages/ru.json';

test('в en те же ключи, что в ru', () => {
  expect(diffKeys(ru, en)).toEqual({ missing: [], extra: [] });
});

test('пустые строки en показываются по-русски', () => {
  expect(withFallback(ru, en)).toEqual(ru);
});
