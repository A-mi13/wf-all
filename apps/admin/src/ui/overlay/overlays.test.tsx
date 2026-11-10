import { screen, within } from '@testing-library/react';
import { type ComponentType, type ReactNode, StrictMode, useRef, useState } from 'react';
import { describe, expect, test, vi } from 'vitest';
import { renderWithIntl } from '../../test/render';
import { Button } from '../Button';
import { Popover } from '../popover/Popover';
import { useToast } from '../toast';
import { ConfirmDialog } from './ConfirmDialog';
import { Drawer } from './Drawer';
import { Modal } from './Modal';
import { Sheet } from './Sheet';

type OverlayProps = {
  open: boolean;
  onClose: () => void;
  title: ReactNode;
  children?: ReactNode;
  footer?: ReactNode;
};

function Harness({
  Overlay,
  children,
}: {
  Overlay: ComponentType<OverlayProps>;
  children?: ReactNode;
}) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button onClick={() => setOpen(true)}>Открыть</Button>
      <Overlay
        open={open}
        onClose={() => setOpen(false)}
        title="Пригласить сотрудника"
        footer={<Button onClick={() => setOpen(false)}>Готово</Button>}
      >
        <label>
          Почта
          <input />
        </label>
        {children}
      </Overlay>
    </>
  );
}

const overlays: [string, ComponentType<OverlayProps>][] = [
  ['Modal', Modal],
  ['Drawer', Drawer],
  ['Sheet', Sheet],
];

describe.each(overlays)('%s', (_, Overlay) => {
  async function open() {
    const r = renderWithIntl(<Harness Overlay={Overlay} />);
    await r.user.click(screen.getByRole('button', { name: 'Открыть' }));
    return { ...r, dialog: screen.getByRole('dialog', { name: 'Пригласить сотрудника' }) };
  }

  test('модальный диалог с заголовком; фокус внутри — на первом поле', async () => {
    const { dialog } = await open();
    expect(dialog).toHaveAttribute('aria-modal', 'true');
    expect(screen.getByLabelText('Почта')).toHaveFocus();
  });

  test('Tab и Shift+Tab не выходят из диалога', async () => {
    const { user, dialog } = await open();
    for (let i = 0; i < 6; i++) {
      await user.tab();
      expect(dialog).toContainElement(document.activeElement as HTMLElement);
    }
    for (let i = 0; i < 6; i++) {
      await user.tab({ shift: true });
      expect(dialog).toContainElement(document.activeElement as HTMLElement);
    }
  });

  test('Esc закрывает, фокус возвращается на кнопку-открыватель', async () => {
    const { user } = await open();
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Открыть' })).toHaveFocus();
  });

  test('кнопка «Закрыть» закрывает и возвращает фокус', async () => {
    const { user, dialog } = await open();
    await user.click(within(dialog).getByRole('button', { name: 'Закрыть' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Открыть' })).toHaveFocus();
  });

  test('прокрутка фона заблокирована, пока диалог открыт', async () => {
    const { user } = await open();
    expect(document.body).toHaveAttribute('data-wf-scroll-lock');
    await user.keyboard('{Escape}');
    expect(document.body).not.toHaveAttribute('data-wf-scroll-lock');
  });
});

test('вложенный диалог: Esc закрывает только верхний, фокус — на его открыватель', async () => {
  function Nested() {
    const [drawer, setDrawer] = useState(false);
    const [confirm, setConfirm] = useState(false);
    return (
      <>
        <Button onClick={() => setDrawer(true)}>Открыть</Button>
        <Drawer open={drawer} onClose={() => setDrawer(false)} title="Сотрудник">
          <Button onClick={() => setConfirm(true)}>Выключить</Button>
          <ConfirmDialog
            open={confirm}
            onClose={() => setConfirm(false)}
            onConfirm={vi.fn()}
            title="Выключить учётку?"
            confirmWord="maria.k@example.com"
            confirmLabel="Выключить"
          />
        </Drawer>
      </>
    );
  }
  const { user } = renderWithIntl(<Nested />);
  await user.click(screen.getByRole('button', { name: 'Открыть' }));
  await user.click(screen.getByRole('button', { name: 'Выключить' }));
  expect(screen.getAllByRole('dialog')).toHaveLength(2);
  await user.keyboard('{Escape}');
  expect(screen.getByRole('dialog', { name: 'Сотрудник' })).toBeInTheDocument();
  expect(screen.queryByRole('dialog', { name: 'Выключить учётку?' })).not.toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Выключить' })).toHaveFocus();
  expect(document.body).toHaveAttribute('data-wf-scroll-lock');
  await user.keyboard('{Escape}');
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  expect(document.body).not.toHaveAttribute('data-wf-scroll-lock');
});

test('поле с autoFocus внутри: открыватель запомнен до него, Esc возвращает фокус на кнопку', async () => {
  function AutoFocus() {
    const [open, setOpen] = useState(false);
    return (
      <>
        <Button onClick={() => setOpen(true)}>Открыть</Button>
        <Modal open={open} onClose={() => setOpen(false)} title="Разблокировка">
          <Button>Раньше поля</Button>
          <input aria-label="Код" autoFocus />
        </Modal>
      </>
    );
  }
  const { user } = renderWithIntl(<AutoFocus />);
  await user.click(screen.getByRole('button', { name: 'Открыть' }));
  // focusInitial не перебивает autoFocus содержимого
  expect(screen.getByLabelText('Код')).toHaveFocus();
  await user.keyboard('{Escape}');
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Открыть' })).toHaveFocus();
});

// main.tsx рендерит под StrictMode: в dev эффект открытия выполняется дважды (с уборкой между
// ними) — второй запуск не должен перебить autoFocus содержимого первым элементом диалога.
test('StrictMode: autoFocus содержимого не перебивается повторным эффектом', async () => {
  function AutoFocus() {
    const [open, setOpen] = useState(false);
    return (
      <>
        <Button onClick={() => setOpen(true)}>Открыть</Button>
        <Modal open={open} onClose={() => setOpen(false)} title="Разблокировка">
          <Button>Раньше поля</Button>
          <input aria-label="Код" autoFocus />
        </Modal>
      </>
    );
  }
  const { user } = renderWithIntl(
    <StrictMode>
      <AutoFocus />
    </StrictMode>,
  );
  await user.click(screen.getByRole('button', { name: 'Открыть' }));
  expect(screen.getByLabelText('Код')).toHaveFocus();
  await user.keyboard('{Escape}');
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Открыть' })).toHaveFocus();
});

test('StrictMode: без autoFocus — первый элемент содержимого', async () => {
  const { user } = renderWithIntl(
    <StrictMode>
      <Harness Overlay={Modal} />
    </StrictMode>,
  );
  await user.click(screen.getByRole('button', { name: 'Открыть' }));
  expect(screen.getByLabelText('Почта')).toHaveFocus();
});

test('тост-ошибка при открытой модалке закрывается с клавиатуры, фокус возвращается в диалог', async () => {
  function WithToast() {
    const toast = useToast();
    return (
      <Modal open onClose={vi.fn()} title="Сотрудник">
        <Button onClick={() => toast.error('Не сохранилось')}>Сохранить</Button>
      </Modal>
    );
  }
  const { user } = renderWithIntl(<WithToast />);
  const dialog = screen.getByRole('dialog', { name: 'Сотрудник' });
  await user.click(screen.getByRole('button', { name: 'Сохранить' }));
  expect(screen.getByText('Не сохранилось')).toBeInTheDocument();
  const close = screen.getByRole('button', { name: 'Закрыть уведомление' });
  for (let i = 0; i < 4 && document.activeElement !== close; i++) await user.tab();
  expect(close).toHaveFocus();
  await user.keyboard('{Enter}');
  expect(screen.queryByText('Не сохранилось')).not.toBeInTheDocument();
  expect(dialog).toContainElement(document.activeElement as HTMLElement);
});

// Popover в общем стеке оверлеев (спека UI-фундамента §4.8, N2, R2-1): немодальный пузырь — Esc по
// верхней из всех, но не блок прокрутки и не ловушка Tab; модальный (календарь) — своя ловушка;
// открытие любого Overlay закрывает все пузыри. В UF2a.6 Esc повторяется на Select.
describe('Popover в стеке оверлеев', () => {
  function Bubble({
    kind = 'menu',
    initiallyOpen = false,
    children,
  }: {
    kind?: 'hint' | 'menu' | 'modal';
    initiallyOpen?: boolean;
    children?: ReactNode;
  }) {
    const anchor = useRef<HTMLButtonElement>(null);
    const [open, setOpen] = useState(initiallyOpen);
    return (
      <>
        <button ref={anchor} type="button" onClick={() => setOpen((v) => !v)}>
          Роль
        </button>
        <Popover
          anchor={anchor}
          open={open}
          kind={kind}
          label="Календарь"
          onClose={() => setOpen(false)}
        >
          {children ?? (
            <div role="listbox" aria-label="Варианты">
              <div role="option" aria-selected="false">
                Модератор
              </div>
            </div>
          )}
        </Popover>
      </>
    );
  }

  test('список в Drawer → Esc → список закрыт, панель открыта, фокус на якоре; второй Esc — панель закрыта', async () => {
    const { user } = renderWithIntl(
      <Drawer open onClose={vi.fn()} title="Сотрудник">
        <Bubble />
      </Drawer>,
    );
    const anchor = screen.getByRole('button', { name: 'Роль' });
    await user.click(anchor);
    expect(screen.getByRole('listbox')).toBeVisible();
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('listbox')).toBeNull();
    expect(screen.getByRole('dialog', { name: 'Сотрудник' })).toBeInTheDocument();
    expect(anchor).toHaveFocus();
  });

  test('второй Esc закрывает панель', async () => {
    function Panel() {
      const [open, setOpen] = useState(true);
      return (
        <Drawer open={open} onClose={() => setOpen(false)} title="Сотрудник">
          <Bubble />
        </Drawer>
      );
    }
    const { user } = renderWithIntl(<Panel />);
    await user.click(screen.getByRole('button', { name: 'Роль' }));
    await user.keyboard('{Escape}');
    expect(screen.getByRole('dialog', { name: 'Сотрудник' })).toBeInTheDocument();
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  test('подсказка открыта на странице — прокрутка не заблокирована', () => {
    renderWithIntl(
      <Bubble kind="hint" initiallyOpen>
        Подсказка
      </Bubble>,
    );
    expect(screen.getByRole('tooltip')).toBeVisible();
    expect(document.body).not.toHaveAttribute('data-wf-scroll-lock');
  });

  test('немодальный в Drawer: Tab с последнего элемента панели — фокус в панели и пузырь закрыт', async () => {
    const { user } = renderWithIntl(
      <Drawer open onClose={vi.fn()} title="Сотрудник">
        <Bubble />
      </Drawer>,
    );
    const dialog = screen.getByRole('dialog', { name: 'Сотрудник' });
    const anchor = screen.getByRole('button', { name: 'Роль' });
    await user.click(anchor);
    expect(screen.getByRole('listbox')).toBeVisible();
    await user.tab();
    expect(dialog).toContainElement(document.activeElement as HTMLElement);
    expect(anchor).not.toHaveFocus();
    expect(screen.queryByRole('listbox')).toBeNull();
  });

  test('пузырь открыт → открыт ConfirmDialog → пузыря нет', async () => {
    function WithConfirm() {
      const [confirm, setConfirm] = useState(false);
      return (
        <>
          <Bubble kind="hint" initiallyOpen>
            Подтверждение вдвоём
          </Bubble>
          <Button onClick={() => setConfirm(true)}>Выключить</Button>
          <ConfirmDialog
            open={confirm}
            onClose={() => setConfirm(false)}
            onConfirm={vi.fn()}
            title="Выключить учётку?"
            confirmWord="maria.k@example.com"
            confirmLabel="Выключить"
          />
        </>
      );
    }
    const { user } = renderWithIntl(<WithConfirm />);
    expect(screen.getByRole('tooltip')).toBeVisible();
    await user.click(screen.getByRole('button', { name: 'Выключить' }));
    expect(screen.getByRole('dialog', { name: 'Выключить учётку?' })).toBeInTheDocument();
    expect(screen.queryByRole('tooltip')).toBeNull();
  });

  test.each([
    ['в Drawer', true],
    ['на странице', false],
  ])(
    'kind modal %s — Tab циклится внутри пузыря, Esc закрывает, фокус на якорь',
    async (_, inDrawer) => {
      const calendar = (
        <Bubble kind="modal">
          <button type="button">1</button>
          <button type="button">2</button>
        </Bubble>
      );
      const { user } = renderWithIntl(
        inDrawer ? (
          <Drawer open onClose={vi.fn()} title="Сотрудник">
            {calendar}
          </Drawer>
        ) : (
          calendar
        ),
      );
      const anchor = screen.getByRole('button', { name: 'Роль' });
      await user.click(anchor);
      const bubble = screen.getByRole('dialog', { name: 'Календарь' });
      expect(bubble).toContainElement(document.activeElement as HTMLElement);
      for (let i = 0; i < 5; i++) {
        await user.tab();
        expect(bubble).toContainElement(document.activeElement as HTMLElement);
      }
      expect(document.body).toHaveAttribute('data-wf-scroll-lock');
      await user.keyboard('{Escape}');
      expect(screen.queryByRole('dialog', { name: 'Календарь' })).toBeNull();
      expect(anchor).toHaveFocus();
      if (inDrawer) expect(screen.getByRole('dialog', { name: 'Сотрудник' })).toBeInTheDocument();
    },
  );
});
