import { getConfig } from '@testing-library/react';
import { expect, test } from 'vitest';

// Флак первого теста файла под нагрузкой пула (ревью V1 m7): холодный старт приложения дольше 1 с — таймаут
// findBy*/waitFor поднят в src/test/setup.ts.
test('таймаут асинхронных ожиданий RTL — 3 с', () => {
  expect(getConfig().asyncUtilTimeout).toBe(3000);
});
