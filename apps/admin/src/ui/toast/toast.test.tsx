import { act, fireEvent, screen, within } from '@testing-library/react';
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
const OK = 'Приглашение ушло на рабочую почту';
const FAIL = 'Не сохранилось. Попробуйте ещё раз';
// тост — элемент с текстом и кнопкой закрытия внутри живой области
const toastOf = (text: string) => screen.getByText(text).parentElement as HTMLElement;

test('живые области постоянны: polite для успеха и справки, assertive для ошибок', () => {
  setup();
  const region = screen.getByRole('region', { name: 'Уведомления' });
  const polite = region.querySelector('[aria-live="polite"]');
  const assertive = region.querySelector('[aria-live="assertive"]');
  expect(polite).toBeInTheDocument();
  expect(assertive).toBeInTheDocument();
  // без ролей status/alert: постоянная область не мешает getByRole('alert') экранов
  expect(within(region).queryByRole('alert')).not.toBeInTheDocument();
  expect(within(region).queryByRole('status')).not.toBeInTheDocument();
  click('Успех');
  click('Ошибка');
  expect(polite).toContainElement(screen.getByText(OK));
  expect(assertive).toContainElement(screen.getByText(FAIL));
});

test('успех исчезает ровно через 8 с, область остаётся', () => {
  expect(TOAST_HIDE_MS).toBe(8000);
  setup();
  click('Успех');
  act(() => vi.advanceTimersByTime(7999));
  expect(screen.getByText(OK)).toBeInTheDocument();
  act(() => vi.advanceTimersByTime(1));
  expect(screen.queryByText(OK)).not.toBeInTheDocument();
  expect(document.querySelector('[aria-live="polite"]')).toBeInTheDocument();
});

test('ошибка сама не исчезает, закрывается руками', () => {
  setup();
  click('Ошибка');
  act(() => vi.advanceTimersByTime(10 * 60_000));
  expect(screen.getByText(FAIL)).toBeInTheDocument();
  click('Закрыть уведомление');
  expect(screen.queryByText(FAIL)).not.toBeInTheDocument();
});

test('действие тоста вызывается и закрывает его; несколько тостов живут независимо', () => {
  const undo = vi.fn();
  setup(undo);
  click('Ошибка');
  click('С действием');
  click('Вернуть');
  expect(undo).toHaveBeenCalledTimes(1);
  expect(screen.queryByText('Флаг переключён')).not.toBeInTheDocument();
  expect(screen.getByText(FAIL)).toBeInTheDocument();
});

test('наведение останавливает таймер успеха, уход — запускает заново', () => {
  setup();
  click('Успех');
  act(() => vi.advanceTimersByTime(5000));
  fireEvent.mouseEnter(toastOf(OK));
  act(() => vi.advanceTimersByTime(20_000));
  expect(screen.getByText(OK)).toBeInTheDocument();
  fireEvent.mouseLeave(toastOf(OK));
  act(() => vi.advanceTimersByTime(7999));
  expect(screen.getByText(OK)).toBeInTheDocument();
  act(() => vi.advanceTimersByTime(1));
  expect(screen.queryByText(OK)).not.toBeInTheDocument();
});

test('новый тост не перезапускает таймер прежнего', () => {
  setup();
  click('Успех');
  act(() => vi.advanceTimersByTime(5000));
  click('Ошибка');
  act(() => vi.advanceTimersByTime(3000));
  expect(screen.queryByText(OK)).not.toBeInTheDocument();
  expect(screen.getByText(FAIL)).toBeInTheDocument();
});
