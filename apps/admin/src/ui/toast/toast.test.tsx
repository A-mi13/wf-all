import { act, fireEvent, screen } from '@testing-library/react';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { renderWithIntl } from '../../test/render';
import { Button } from '../Button';
import { TOAST_HIDE_MS, useToast } from './index';

function Harness({ undo }: { undo?: () => void }) {
  const toast = useToast();
  return (
    <>
      <Button onClick={() => toast.success('Приглашение ушло на рабочую почту')}>Успех</Button>
      <Button onClick={() => toast.error('Не сохранилось. Попробуйте ещё раз')}>Ошибка</Button>
      <Button
        onClick={() =>
          toast.success('Флаг переключён', undo && { action: { label: 'Вернуть', onClick: undo } })
        }
      >
        С действием
      </Button>
    </>
  );
}

beforeEach(() => {
  vi.useFakeTimers();
});
afterEach(() => {
  vi.useRealTimers();
});

// fireEvent, не user-event: с фейковыми таймерами user-event ждёт настоящий setTimeout
const setup = (undo?: () => void) => renderWithIntl(<Harness undo={undo} />);
const click = (name: string) => fireEvent.click(screen.getByRole('button', { name }));

test('успех — status, исчезает ровно через 8 с', () => {
  expect(TOAST_HIDE_MS).toBe(8000);
  setup();
  click('Успех');
  const region = screen.getByRole('region', { name: 'Уведомления' });
  expect(region).toContainElement(screen.getByRole('status'));
  expect(screen.getByRole('status')).toHaveTextContent('Приглашение ушло на рабочую почту');
  act(() => vi.advanceTimersByTime(7999));
  expect(screen.getByRole('status')).toBeInTheDocument();
  act(() => vi.advanceTimersByTime(1));
  expect(screen.queryByRole('status')).not.toBeInTheDocument();
});

test('ошибка — alert, сама не исчезает, закрывается руками', () => {
  setup();
  click('Ошибка');
  act(() => vi.advanceTimersByTime(10 * 60_000));
  const alert = screen.getByRole('alert');
  expect(alert).toHaveTextContent('Не сохранилось. Попробуйте ещё раз');
  click('Закрыть уведомление');
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});

test('действие тоста вызывается и закрывает его; несколько тостов живут независимо', () => {
  const undo = vi.fn();
  setup(undo);
  click('Ошибка');
  click('С действием');
  click('Вернуть');
  expect(undo).toHaveBeenCalledTimes(1);
  expect(screen.queryByText('Флаг переключён')).not.toBeInTheDocument();
  expect(screen.getByRole('alert')).toBeInTheDocument();
});

test('наведение останавливает таймер успеха, уход — запускает заново', () => {
  setup();
  click('Успех');
  act(() => vi.advanceTimersByTime(5000));
  fireEvent.mouseEnter(screen.getByRole('status'));
  act(() => vi.advanceTimersByTime(20_000));
  expect(screen.getByRole('status')).toBeInTheDocument();
  fireEvent.mouseLeave(screen.getByRole('status'));
  act(() => vi.advanceTimersByTime(7999));
  expect(screen.getByRole('status')).toBeInTheDocument();
  act(() => vi.advanceTimersByTime(1));
  expect(screen.queryByRole('status')).not.toBeInTheDocument();
});

test('новый тост не перезапускает таймер прежнего', () => {
  setup();
  click('Успех');
  act(() => vi.advanceTimersByTime(5000));
  click('Ошибка');
  act(() => vi.advanceTimersByTime(3000));
  expect(screen.queryByRole('status')).not.toBeInTheDocument();
  expect(screen.getByRole('alert')).toBeInTheDocument();
});
