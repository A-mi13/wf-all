import { act, fireEvent, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { renderWithIntl } from '../../test/render';
import { Button } from '../Button';
import { Drawer } from '../overlay';
import { RelativeTime } from './RelativeTime';
import { NowProvider, useNow } from './useNow';

// Время (спека UI-фундамента §4.7, план UF2a.9): RelativeTime — «5 мин назад» в <time dateTime>, точное — скрытым
// текстом внутри <time> и подсказкой по наведению и по фокусу внутри строки; своей остановки Tab нет. useNow —
// один таймер на гранулу на всё приложение, в скрытой вкладке стоит.

const NOW = new Date('2026-10-28T12:00:00Z');
const ago = (ms: number) => new Date(NOW.getTime() - ms).toISOString();
const SEC = 1000;
const MIN = 60 * SEC;
const HOUR = 60 * MIN;
const DAY = 24 * HOUR;

let visibility: DocumentVisibilityState = 'visible';
beforeEach(() => {
  visibility = 'visible';
  vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() => visibility);
});
afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

const at = (value: string, mode?: 'auto' | 'relative' | 'exact') =>
  renderWithIntl(
    <NowProvider now={NOW}>
      <RelativeTime value={value} mode={mode} />
    </NowProvider>,
    { timeZone: 'Europe/Moscow' },
  );

const visible = (el: HTMLElement) => el.querySelector('[data-relative]')?.textContent;

describe('auto: границы', () => {
  test.each([
    [59 * SEC, 'только что'],
    [1 * MIN, '1 минуту назад'],
    [59 * MIN, '59 минут назад'],
    [1 * HOUR, '1 час назад'],
    [6 * DAY + 23 * HOUR, '6 дней назад'],
  ])('%i мс назад — «%s»', (ms, want) => {
    at(ago(ms));
    expect(visible(document.body)).toBe(want);
  });

  test('7 дней — уже дата, а не «7 дней назад»', () => {
    at(ago(7 * DAY));
    expect(visible(document.body)).toBe('21 окт. 2026 г.');
  });

  // Ревью I1: снимок useNow('minute') отстаёт от настоящего до минуты, часы клиента и сервера расходятся —
  // свежая запись «из будущего» меньше минуты — тоже «только что», не «через 29 секунд».
  test('запись новее снимка «сейчас» (< 1 мин вперёд) — «только что»', () => {
    at(new Date(NOW.getTime() + 29 * SEC).toISOString());
    expect(visible(document.body)).toBe('только что');
  });

  test('свежая запись через 30 с после тика минуты — «только что»', () => {
    vi.useFakeTimers({ now: NOW });
    function Clock() {
      return <i>{useNow('minute').toISOString()}</i>;
    }
    const { rerender } = renderWithIntl(<Clock />, { timeZone: 'Europe/Moscow' });
    act(() => vi.advanceTimersByTime(30 * SEC));
    const created = new Date(Date.now() - SEC).toISOString();
    rerender(
      <>
        <Clock />
        <RelativeTime value={created} />
      </>,
    );
    expect(visible(document.body)).toBe('только что');
  });

  test('будущее — «через …»', () => {
    at(new Date(NOW.getTime() + 5 * MIN).toISOString());
    expect(visible(document.body)).toBe('через 5 минут');
  });

  test('relative — относительно и старше недели; exact — точное видимым текстом', () => {
    const { unmount } = at(ago(10 * DAY), 'relative');
    expect(visible(document.body)).toBe('10 дней назад');
    unmount();
    at(ago(5 * MIN), 'exact');
    expect(screen.getByText(/28 окт\. 2026 г\., 14:55:00/)).toBeVisible();
  });
});

describe('доступность', () => {
  test('<time dateTime>, точное время — скрытым текстом внутри <time>', () => {
    const iso = ago(5 * MIN);
    const { container } = at(iso);
    const time = container.querySelector('time')!;
    expect(time).toHaveAttribute('datetime', iso);
    expect(time).toHaveTextContent('5 минут назад');
    expect(time.querySelector('[data-exact]')).toHaveTextContent(/28 окт\. 2026 г\., 14:55:00/);
    // не hidden и не display: none — читалка слышит его сразу (скрыт только визуально, классом srOnly)
    expect(time.querySelector('[data-exact]')).toBeVisible();
    expect(time).not.toHaveAttribute('title');
  });

  test('своей остановки Tab нет', () => {
    const { container } = at(ago(5 * MIN));
    for (const el of container.querySelectorAll('*'))
      expect(el.hasAttribute('tabindex') && el.getAttribute('tabindex') !== '-1').toBe(false);
    expect(container.querySelector('a, button, input')).toBeNull();
  });

  test('подсказка — по фокусу внутри строки, уходит с фокусом', () => {
    renderWithIntl(
      <NowProvider now={NOW}>
        <table>
          <tbody>
            <tr>
              <td>
                <RelativeTime value={ago(5 * MIN)} />
              </td>
              <td>
                <Button>Подробнее</Button>
              </td>
            </tr>
          </tbody>
        </table>
        <Button>Снаружи</Button>
      </NowProvider>,
      { timeZone: 'Europe/Moscow' },
    );
    expect(screen.queryByRole('tooltip')).toBeNull();
    act(() => screen.getByRole('button', { name: 'Подробнее' }).focus());
    expect(screen.getByRole('tooltip')).toHaveTextContent(/28 окт\. 2026 г\., 14:55:00/);
    act(() => screen.getByRole('button', { name: 'Снаружи' }).focus());
    expect(screen.queryByRole('tooltip')).toBeNull();
  });

  // Ревью I2: подсказка по фокусу строки закрывается Esc (WCAG 1.4.13) и не съедает Esc панели: первый Esc —
  // подсказка, второй — панель; новый фокус в строке открывает её снова.
  test('Esc закрывает подсказку по фокусу строки, следующий Esc доходит до панели', () => {
    const onClose = vi.fn();
    renderWithIntl(
      <NowProvider now={NOW}>
        <Drawer open onClose={onClose} title="Сотрудник">
          <table>
            <tbody>
              <tr>
                <td>
                  <RelativeTime value={ago(5 * MIN)} />
                </td>
                <td>
                  <Button>Завершить</Button>
                  <Button>Ещё</Button>
                </td>
              </tr>
            </tbody>
          </table>
        </Drawer>
      </NowProvider>,
      { timeZone: 'Europe/Moscow' },
    );
    act(() => screen.getByRole('button', { name: 'Завершить' }).focus());
    expect(screen.getByRole('tooltip')).toBeInTheDocument();
    fireEvent.keyDown(document.activeElement!, { key: 'Escape' });
    expect(screen.queryByRole('tooltip')).toBeNull();
    expect(screen.getByRole('button', { name: 'Завершить' })).toHaveFocus();
    expect(onClose).not.toHaveBeenCalled();
    fireEvent.keyDown(document.activeElement!, { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(1);
    act(() => screen.getByRole('button', { name: 'Ещё' }).focus());
    expect(screen.getByRole('tooltip')).toBeInTheDocument();
  });

  // Ревью m7: два времени в одной строке («создан», «изменён») — по фокусу строки подсказка одна (у первого),
  // точное время второго — скрытым текстом и наведением.
  test('два времени в строке — по фокусу одна подсказка', () => {
    renderWithIntl(
      <NowProvider now={NOW}>
        <table>
          <tbody>
            <tr>
              <td>
                <RelativeTime value={ago(5 * MIN)} />
              </td>
              <td>
                <RelativeTime value={ago(3 * HOUR)} />
              </td>
              <td>
                <Button>Подробнее</Button>
              </td>
            </tr>
          </tbody>
        </table>
      </NowProvider>,
      { timeZone: 'Europe/Moscow' },
    );
    act(() => screen.getByRole('button', { name: 'Подробнее' }).focus());
    expect(screen.getAllByRole('tooltip')).toHaveLength(1);
    expect(screen.getByRole('tooltip')).toHaveTextContent(/14:55:00/);
  });

  test('подсказка — по наведению мышью', () => {
    const { container } = at(ago(5 * MIN));
    fireEvent.pointerEnter(container.querySelector('time')!.parentElement!, {
      pointerType: 'mouse',
    });
    expect(screen.getByRole('tooltip')).toHaveTextContent(/14:55:00/);
  });
});

describe('useNow', () => {
  function Clock({ id }: { id: number }) {
    const now = useNow('minute');
    return <p data-testid={`clock-${id}`}>{now.toISOString()}</p>;
  }

  test('один таймер на много подписчиков; идёт по минутам', () => {
    vi.useFakeTimers({ now: NOW });
    const { unmount } = renderWithIntl(
      <>
        {Array.from({ length: 20 }, (_, i) => (
          <Clock key={i} id={i} />
        ))}
      </>,
    );
    expect(vi.getTimerCount()).toBe(1);
    act(() => vi.advanceTimersByTime(MIN));
    expect(screen.getByTestId('clock-0')).toHaveTextContent('2026-10-28T12:01:00');
    expect(screen.getByTestId('clock-19')).toHaveTextContent('2026-10-28T12:01:00');
    expect(vi.getTimerCount()).toBe(1);
    unmount();
    expect(vi.getTimerCount()).toBe(0);
  });

  test('в скрытой вкладке стоит; вернулись — сразу текущее время и снова тикает', () => {
    vi.useFakeTimers({ now: NOW });
    renderWithIntl(<Clock id={0} />);
    visibility = 'hidden';
    act(() => {
      document.dispatchEvent(new Event('visibilitychange'));
    });
    expect(vi.getTimerCount()).toBe(0);
    act(() => vi.advanceTimersByTime(10 * MIN));
    expect(screen.getByTestId('clock-0')).toHaveTextContent('2026-10-28T12:00:00');
    visibility = 'visible';
    act(() => {
      document.dispatchEvent(new Event('visibilitychange'));
    });
    expect(screen.getByTestId('clock-0')).toHaveTextContent('2026-10-28T12:10:00');
    expect(vi.getTimerCount()).toBe(1);
  });

  test('RelativeTime обновляется сам: «только что» → «1 минуту назад»', () => {
    vi.useFakeTimers({ now: NOW });
    renderWithIntl(<RelativeTime value={NOW.toISOString()} />, { timeZone: 'Europe/Moscow' });
    expect(visible(document.body)).toBe('только что');
    act(() => vi.advanceTimersByTime(MIN));
    expect(visible(document.body)).toBe('1 минуту назад');
  });

  test('NowProvider — время зафиксировано, таймера нет', () => {
    vi.useFakeTimers({ now: NOW });
    renderWithIntl(
      <NowProvider now={new Date('2026-01-01T00:00:00Z')}>
        <Clock id={0} />
      </NowProvider>,
    );
    expect(screen.getByTestId('clock-0')).toHaveTextContent('2026-01-01T00:00:00');
    expect(vi.getTimerCount()).toBe(0);
  });
});
