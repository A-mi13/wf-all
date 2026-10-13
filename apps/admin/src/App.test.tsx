import { render, screen } from '@testing-library/react';
import { expect, test } from 'vitest';
import { App } from './App';

// R13: /v1/health удалён из контракта admin; временный App (до Task 11) связь не проверяет —
// проверкой станет GET /v1/staff/me.
test('App показывает заголовок без проверки связи', () => {
  render(<App />);
  expect(screen.getByRole('heading', { name: 'Администрирование' })).toBeInTheDocument();
  // строки «Проверяем API…» нет
  expect(screen.queryByRole('status')).toBeNull();
});
