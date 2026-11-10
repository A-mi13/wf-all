import { screen, within } from '@testing-library/react';
import { afterEach, describe, expect, test } from 'vitest';
import { renderWithIntl } from '../../test/render';
import { THEME_STORAGE_KEY } from './context';
import { ThemeProvider } from './ThemeProvider';
import { ThemeSwitch } from './ThemeSwitch';

// Переключатель темы (спека §4.12 «ThemeSwitch»): Segmented «Тема» из трёх сегментов-значков (монитор,
// солнце, луна); у значка видимого текста нет — имя для читалки и title для мыши.

afterEach(() => {
  localStorage.clear();
  delete document.documentElement.dataset.theme;
});

const mount = (locale: 'ru' | 'en' = 'ru') =>
  renderWithIntl(
    <ThemeProvider>
      <ThemeSwitch />
    </ThemeProvider>,
    { locale },
  );

describe('ThemeSwitch', () => {
  test('группа «Тема» из трёх кнопок с именами и подсказками; значок декоративный', () => {
    mount();
    const group = screen.getByRole('group', { name: 'Тема' });
    const buttons = within(group).getAllByRole('button');
    expect(buttons.map((b) => b.getAttribute('aria-label'))).toEqual([
      'Система',
      'Светлая',
      'Тёмная',
    ]);
    for (const b of buttons) {
      expect(b).toHaveAttribute('title', b.getAttribute('aria-label'));
      expect(b.querySelector('svg[aria-hidden="true"]')).not.toBeNull();
      expect(b.textContent).toBe(''); // видимого текста нет — только значок
    }
  });

  test('нажата текущая тема (по умолчанию — «Система»)', () => {
    mount();
    expect(screen.getByRole('button', { name: 'Система' })).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByRole('button', { name: 'Тёмная' })).toHaveAttribute('aria-pressed', 'false');
  });

  test('клик — setTheme: атрибут, storage и нажатая кнопка', async () => {
    const { user } = mount();
    await user.click(screen.getByRole('button', { name: 'Светлая' }));
    expect(document.documentElement.dataset.theme).toBe('light');
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe('light');
    expect(screen.getByRole('button', { name: 'Светлая' })).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByRole('button', { name: 'Система' })).toHaveAttribute(
      'aria-pressed',
      'false',
    );
  });

  test('английский интерфейс', () => {
    mount('en');
    const group = screen.getByRole('group', { name: 'Theme' });
    expect(
      within(group)
        .getAllByRole('button')
        .map((b) => b.getAttribute('aria-label')),
    ).toEqual(['System', 'Light', 'Dark']);
  });
});
