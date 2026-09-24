// Vitest — для чистых функций и клиентских компонентов. Async Server Components он не рендерит:
// их покрывает Playwright (e2e/).
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vitest/config';

export default defineConfig({
  plugins: [react()],
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    include: ['src/**/*.test.{ts,tsx}'],
  },
});
