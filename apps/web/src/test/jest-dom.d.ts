// Vitest 5 расширяет Matchers<R, T>, а jest-dom 7.0.1 — старый Assertion<T>. Мост типов.
import 'vitest';
import type { TestingLibraryMatchers } from '@testing-library/jest-dom/matchers';

declare module 'vitest' {
  // eslint-disable-next-line @typescript-eslint/no-empty-object-type
  interface Matchers<R = unknown, T = unknown> extends TestingLibraryMatchers<T, R> {}
}
