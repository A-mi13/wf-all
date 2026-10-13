import { loadErrorCodes } from '@wf/contracts/error-codes';
import { missingKeys } from '@wf/i18n';
import { expect, test } from 'vitest';
import en from '../../messages/en.json';
import ru from '../../messages/ru.json';

test('у каждого кода ошибки контракта админки есть текст', async () => {
  const codes = await loadErrorCodes('admin');
  expect(codes).toContain('validation.failed');
  expect(
    missingKeys(
      ru,
      codes.map((c) => `errors.${c}`),
    ),
  ).toEqual([]);
});

// D21: в админке en — перевод, а не пустая строка с откатом на ru: иначе переключатель языка
// показывал бы ошибки по-русски.
test('у каждого кода ошибки контракта админки есть перевод en', async () => {
  const codes = await loadErrorCodes('admin');
  expect(codes).toContain('auth.forbidden');
  expect(
    missingKeys(
      en,
      codes.map((c) => `errors.${c}`),
    ),
  ).toEqual([]);
});
