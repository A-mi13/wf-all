import { act, screen } from '@testing-library/react';
import { useState } from 'react';
import { expect, test } from 'vitest';
import { renderWithIntl } from '../../test/render';
import { Button } from '../Button';
import { Modal } from '../overlay';
import { type ToastApi, useToast } from './index';

// Закрыли тост, на котором стоял фокус (кнопкой или программно), — фокус возвращается туда, откуда
// пришёл в тост; того элемента уже нет — на панель верхнего диалога по стеку оверлеев.

const FAIL = 'Не сохранилось';
const closeToast = () => screen.getByRole('button', { name: 'Закрыть уведомление' });

// Tab до кнопки закрытия тоста (не больше n нажатий).
async function tabToToast(user: ReturnType<typeof renderWithIntl>['user'], n = 6) {
  for (let i = 0; i < n && document.activeElement !== closeToast(); i++) await user.tab();
  expect(closeToast()).toHaveFocus();
}

test('без диалога: после закрытия тоста фокус — на элемент, с которого ушли в тост', async () => {
  function Page() {
    const toast = useToast();
    return <Button onClick={() => toast.error(FAIL)}>Сохранить</Button>;
  }
  const { user } = renderWithIntl(<Page />);
  const save = screen.getByRole('button', { name: 'Сохранить' });
  await user.click(save);
  await tabToToast(user);
  await user.keyboard('{Enter}');
  expect(screen.queryByText(FAIL)).not.toBeInTheDocument();
  expect(save).toHaveFocus();
});

test('в диалоге: фокус возвращается на кнопку диалога, с которой ушли в тост', async () => {
  function Page() {
    const toast = useToast();
    return (
      <Modal open onClose={() => {}} title="Сотрудник">
        <Button onClick={() => toast.error(FAIL)}>Сохранить</Button>
      </Modal>
    );
  }
  const { user } = renderWithIntl(<Page />);
  const save = screen.getByRole('button', { name: 'Сохранить' });
  await user.click(save);
  await tabToToast(user);
  await user.keyboard('{Enter}');
  expect(save).toHaveFocus();
});

test('элемента, с которого пришли, уже нет — фокус на панель верхнего диалога', async () => {
  const ctl: { hide?: () => void } = {};
  function Page() {
    const toast = useToast();
    const [shown, setShown] = useState(true);
    ctl.hide = () => setShown(false);
    return (
      <Modal open onClose={() => {}} title="Сотрудник">
        <p>Карточка</p>
        {shown && <Button onClick={() => toast.error(FAIL)}>Сохранить</Button>}
      </Modal>
    );
  }
  const { user } = renderWithIntl(<Page />);
  await user.click(screen.getByRole('button', { name: 'Сохранить' }));
  await tabToToast(user);
  act(() => ctl.hide?.());
  await user.keyboard('{Enter}');
  expect(screen.getByRole('dialog', { name: 'Сотрудник' })).toHaveFocus();
});

test('программный dismiss(id) при фокусе на тосте — фокус туда, откуда пришли', async () => {
  const ctl: { toast?: ToastApi; id?: number } = {};
  function Page() {
    const toast = useToast();
    ctl.toast = toast;
    return <Button onClick={() => (ctl.id = toast.error(FAIL))}>Сохранить</Button>;
  }
  const { user } = renderWithIntl(<Page />);
  const save = screen.getByRole('button', { name: 'Сохранить' });
  await user.click(save);
  await tabToToast(user);
  act(() => ctl.toast?.dismiss(ctl.id ?? -1));
  expect(screen.queryByText(FAIL)).not.toBeInTheDocument();
  expect(save).toHaveFocus();
});

test('программный dismiss без фокуса на тосте фокус не трогает', async () => {
  const ctl: { toast?: ToastApi; id?: number } = {};
  function Page() {
    const toast = useToast();
    ctl.toast = toast;
    return (
      <>
        <Button onClick={() => (ctl.id = toast.error(FAIL))}>Сохранить</Button>
        <input aria-label="Поиск" />
      </>
    );
  }
  const { user } = renderWithIntl(<Page />);
  await user.click(screen.getByRole('button', { name: 'Сохранить' }));
  await user.click(screen.getByLabelText('Поиск'));
  act(() => ctl.toast?.dismiss(ctl.id ?? -1));
  expect(screen.getByLabelText('Поиск')).toHaveFocus();
});
