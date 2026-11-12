import { screen } from '@testing-library/react';
import { describe, expect, test, vi } from 'vitest';
import { renderWithIntl } from '../../test/render';
import { TodoList } from './TodoList';

// Список дел главной (спека UI-фундамента §4.13, план UF4.2): строки-ссылки; пусто — иллюстрация и «Всё спокойно».
describe('TodoList', () => {
  test('строки — ссылки с адресом и переходом приложения', async () => {
    const onNavigate = vi.fn();
    const { user } = renderWithIntl(
      <TodoList
        items={[
          { id: 'a', text: 'Разобрать 3 запроса', href: '/approvals', onNavigate },
          {
            id: 'b',
            text: 'Осталось 2 резервных кода',
            href: '/me',
            tone: 'warning',
            onNavigate: () => undefined,
          },
        ]}
      />,
    );
    const links = screen.getAllByRole('link');
    expect(links.map((l) => [l.textContent, l.getAttribute('href')])).toEqual([
      ['Разобрать 3 запроса', '/approvals'],
      ['Осталось 2 резервных кода', '/me'],
    ]);
    await user.click(links[0] as HTMLElement);
    expect(onNavigate).toHaveBeenCalledTimes(1);
    expect(screen.queryByText('Всё спокойно')).toBeNull();
  });

  test('пусто — «Всё спокойно»', () => {
    renderWithIntl(<TodoList items={[]} />);
    expect(screen.getByText('Всё спокойно')).toBeInTheDocument();
    expect(screen.queryByRole('link')).toBeNull();
  });

  test('ошибка — строка «Не загрузилось · Повторить», «Всё спокойно» не говорится', async () => {
    const onRetry = vi.fn();
    const { user } = renderWithIntl(<TodoList items={[]} error={{ onRetry }} />);
    expect(screen.getByText('Не загрузилось')).toBeInTheDocument();
    expect(screen.queryByText('Всё спокойно')).toBeNull();
    await user.click(screen.getByRole('button', { name: 'Повторить' }));
    expect(onRetry).toHaveBeenCalledTimes(1);
  });

  test('ошибка под делами — дела видны', () => {
    renderWithIntl(
      <TodoList
        items={[
          { id: 'a', text: 'Разобрать 3 запроса', href: '/approvals', onNavigate: () => undefined },
        ]}
        error={{ onRetry: () => undefined }}
      />,
    );
    expect(screen.getByRole('link', { name: 'Разобрать 3 запроса' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Повторить' })).toBeInTheDocument();
  });
});
