import { screen } from '@testing-library/react';
import { describe, expect, test, vi } from 'vitest';
import { renderWithIntl } from '../test/render';
import { Avatar } from './Avatar';
import { Button } from './Button';
import { CountBadge } from './CountBadge';
import { StatusChip } from './StatusChip';

describe('Button', () => {
  test('по умолчанию type="button": не отправляет форму случайно', async () => {
    const onSubmit = vi.fn((e: SubmitEvent) => e.preventDefault());
    const { user } = renderWithIntl(
      <form onSubmit={(e) => onSubmit(e.nativeEvent as SubmitEvent)}>
        <Button>Сохранить</Button>
      </form>,
    );
    await user.click(screen.getByRole('button', { name: 'Сохранить' }));
    expect(onSubmit).not.toHaveBeenCalled();
  });

  test('loading: недоступна, aria-busy, клик не доходит', async () => {
    const onClick = vi.fn();
    const { user } = renderWithIntl(
      <Button loading onClick={onClick}>
        Сохранить
      </Button>,
    );
    const b = screen.getByRole('button', { name: 'Сохранить' });
    expect(b).toBeDisabled();
    expect(b).toHaveAttribute('aria-busy', 'true');
    await user.click(b);
    expect(onClick).not.toHaveBeenCalled();
  });

  test('disabled и варианты', () => {
    renderWithIntl(
      <>
        <Button variant="danger" disabled>
          Удалить
        </Button>
        <Button variant="secondary">Отмена</Button>
      </>,
    );
    expect(screen.getByRole('button', { name: 'Удалить' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Отмена' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Отмена' })).not.toHaveAttribute('aria-busy');
  });
});

describe('StatusChip, CountBadge, Avatar', () => {
  test('StatusChip — текст рядом с цветом (цвет не единственный носитель смысла)', () => {
    renderWithIntl(<StatusChip tone="success">2ФА включена</StatusChip>);
    expect(screen.getByText('2ФА включена')).toBeInTheDocument();
  });

  test('CountBadge: 0 не показывается, больше 99 — «99+»', () => {
    const { rerender, container } = renderWithIntl(<CountBadge count={0} />);
    expect(container).toBeEmptyDOMElement();
    rerender(<CountBadge count={7} />);
    expect(screen.getByText('7')).toBeInTheDocument();
    rerender(<CountBadge count={140} />);
    expect(screen.getByText('99+')).toBeInTheDocument();
  });

  test('CountBadge с подписью: имя — визуально скрытым текстом, не aria-label', () => {
    const { container } = renderWithIntl(<CountBadge count={3} label="3 ожидают решения" />);
    const badge = container.querySelector('span');
    expect(badge).not.toHaveAttribute('aria-label');
    expect(screen.getByText('3 ожидают решения')).toBeInTheDocument();
    expect(screen.getByText('3')).toHaveAttribute('aria-hidden', 'true');
  });

  test('Avatar — инициалы из имени, декоративный', () => {
    const { rerender, container } = renderWithIntl(<Avatar name="Мария К." />);
    expect(container).toHaveTextContent('МК');
    expect(container.firstElementChild).toHaveAttribute('aria-hidden', 'true');
    rerender(<Avatar name="  алексей  " />);
    expect(container).toHaveTextContent(/^А$/);
    rerender(<Avatar name="" />);
    expect(container).toHaveTextContent('?');
  });
});
