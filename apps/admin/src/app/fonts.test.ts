import { expect, test } from 'vitest';

// Начертания @fontsource разрешаются из пакетов (опечатка в пути или пропавший пакет — падение
// здесь, а не только в сборке).
test('шрифты подключаются из @fontsource', async () => {
  await expect(import('./fonts')).resolves.toBeDefined();
});
