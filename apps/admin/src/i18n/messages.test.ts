import { diffKeys, type Messages } from '@wf/i18n';
import { expect, test } from 'vitest';
import en from '../../messages/en.json';
import ru from '../../messages/ru.json';
import { messagesFor } from './messages';

test('в en те же ключи, что в ru', () => {
  expect(diffKeys(ru, en)).toEqual({ missing: [], extra: [] });
});

// Текст по пути «a.b.c»; группа или нет пути — undefined.
const at = (m: Messages, path: string): unknown =>
  path.split('.').reduce<unknown>((v, k) => (v as Messages | undefined)?.[k], m);

const paths = (m: Messages, prefix = ''): string[] =>
  Object.entries(m).flatMap(([k, v]) =>
    typeof v === 'string' ? [`${prefix}${k}`] : paths(v, `${prefix}${k}.`),
  );

test('en: перевод, где он есть; пустая строка en — текст ru', () => {
  const merged = messagesFor('en');
  for (const path of paths(ru)) {
    const own = at(en, path);
    expect(at(merged, path), path).toBe(own === '' ? at(ru, path) : own);
  }
});
