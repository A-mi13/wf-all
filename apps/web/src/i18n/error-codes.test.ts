import { loadErrorCodes } from '@wf/contracts/error-codes';
import { missingKeys } from '@wf/i18n';
import { expect, test } from 'vitest';
import ru from '../../messages/ru.json';

test('у каждого кода ошибки публичного контракта есть текст', async () => {
  const codes = await loadErrorCodes('public');
  expect(codes).toContain('validation.failed');
  expect(
    missingKeys(
      ru,
      codes.map((c) => `errors.${c}`),
    ),
  ).toEqual([]);
});
