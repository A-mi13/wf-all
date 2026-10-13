import { screen, within } from '@testing-library/react';
import { type ComponentType, type ReactNode, useState } from 'react';
import { describe, expect, test, vi } from 'vitest';
import { renderWithIntl } from '../../test/render';
import { Button } from '../Button';
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
