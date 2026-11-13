import { act, fireEvent, screen, within } from '@testing-library/react';
import { useRef, useState } from 'react';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { renderWithIntl } from '../../test/render';
import { Button } from '../Button';
import { Drawer } from '../overlay/Drawer';
import { Popover } from '../popover/Popover';
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

// UF2b.6 (спека UI-фундамента §4.10, §4.8 m-h, n5): тост с «Отменить»
function UndoHarness({ undo }: { undo: () => Promise<void> }) {
  const toast = useToast();
  const anchor = useRef<HTMLButtonElement>(null);
  const [hint, setHint] = useState(false);
  const [drawer, setDrawer] = useState(false);
  return (
    <>
      <Button onClick={() => toast.success('Район в архиве', { undo })}>Архив</Button>
      <Button onClick={() => toast.success('Флаг выключен', { undo })}>Флаг</Button>
      <Button onClick={() => toast.success(OK)}>Успех</Button>
      <Button onClick={() => toast.dismissUndo()}>Снять отмену</Button>
      <Button onClick={() => setDrawer(true)}>Панель</Button>
      <button ref={anchor} type="button" onClick={() => setHint(true)}>
        Подсказка
      </button>
      <Popover anchor={anchor} open={hint} kind="hint" onClose={() => setHint(false)}>
        Текст подсказки
      </Popover>
      <Drawer open={drawer} onClose={() => setDrawer(false)} title="Район">
        <p>Тело</p>
      </Drawer>
    </>
  );
}

describe('тост с «Отменить»', () => {
  const setupUndo = (undo = vi.fn(async () => {})) => ({
    undo,
    ...renderWithIntl(<UndoHarness undo={undo} />),
  });
  const esc = () => fireEvent.keyDown(document.activeElement ?? document.body, { key: 'Escape' });
  const html = document.documentElement;

  test('сам не исчезает; data-toast-open на корне, пока он на экране', () => {
    setupUndo();
    expect(html).not.toHaveAttribute('data-toast-open');
    click('Архив');
    act(() => vi.advanceTimersByTime(10 * 60_000));
    expect(screen.getByText('Район в архиве')).toBeInTheDocument();
    expect(html).toHaveAttribute('data-toast-open');
    click('Закрыть уведомление');
    expect(screen.queryByText('Район в архиве')).not.toBeInTheDocument();
    expect(html).not.toHaveAttribute('data-toast-open');
  });

  test('обычный успех data-toast-open не ставит', () => {
    setupUndo();
    click('Успех');
    expect(html).not.toHaveAttribute('data-toast-open');
  });

  test('«Отменить» вызывает отмену один раз и снимает тост', () => {
    const { undo } = setupUndo();
    click('Архив');
    const button = screen.getByRole('button', { name: 'Отменить' });
    fireEvent.click(button);
    fireEvent.click(button);
    expect(undo).toHaveBeenCalledTimes(1);
    expect(screen.queryByText('Район в архиве')).not.toBeInTheDocument();
    expect(html).not.toHaveAttribute('data-toast-open');
  });

  test('новый тост с «Отменить» заменяет прежний', () => {
    setupUndo();
    click('Архив');
    click('Флаг');
    expect(screen.queryByText('Район в архиве')).not.toBeInTheDocument();
    expect(screen.getByText('Флаг выключен')).toBeInTheDocument();
    expect(screen.getAllByRole('button', { name: 'Отменить' })).toHaveLength(1);
  });

  test('dismissUndo снимает тосты с отменой, остальные живут', () => {
    setupUndo();
    click('Успех');
    click('Архив');
    click('Снять отмену');
    expect(screen.queryByText('Район в архиве')).not.toBeInTheDocument();
    expect(screen.getByText(OK)).toBeInTheDocument();
    expect(html).not.toHaveAttribute('data-toast-open');
  });

  test('Esc без оверлеев закрывает новейший тост', () => {
    setupUndo();
    click('Успех');
    click('Архив');
    esc();
    expect(screen.queryByText('Район в архиве')).not.toBeInTheDocument();
    expect(screen.getByText(OK)).toBeInTheDocument();
  });

  test('подсказка открыта + тост: Esc закрывает подсказку, тост остаётся; второй Esc — тост (m-h)', () => {
    setupUndo();
    click('Архив');
    click('Подсказка');
    expect(screen.getByRole('tooltip')).toBeInTheDocument();
    esc();
    expect(screen.queryByRole('tooltip')).not.toBeInTheDocument();
    expect(screen.getByText('Район в архиве')).toBeInTheDocument();
    esc();
    expect(screen.queryByText('Район в архиве')).not.toBeInTheDocument();
  });

  test('при открытом Drawer Esc закрывает панель, тост не трогает', () => {
    setupUndo();
    click('Архив');
    click('Панель');
    expect(screen.getByRole('dialog', { name: 'Район' })).toBeInTheDocument();
    esc();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.getByText('Район в архиве')).toBeInTheDocument();
  });
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
