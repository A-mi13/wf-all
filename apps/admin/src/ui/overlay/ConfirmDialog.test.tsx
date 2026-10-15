import { screen, within } from '@testing-library/react';
import { useState } from 'react';
import { expect, test, vi } from 'vitest';
import { ApiError } from '../../api/client';
import { renderWithIntl } from '../../test/render';
import { Button } from '../Button';
import { ConfirmDialog } from './ConfirmDialog';

function Harness(props: { onConfirm: () => void; word: string }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button onClick={() => setOpen(true)}>Сбросить доступ</Button>
      <ConfirmDialog
        open={open}
        onClose={() => setOpen(false)}
        onConfirm={props.onConfirm}
        title="Сбросить доступ Марии?"
        text="Пароль и второй фактор перестанут работать."
        consequences={['Все сеансы завершатся', 'Придёт письмо с новым приглашением']}
        confirmWord={props.word}
        confirmLabel="Сбросить"
      />
    </>
  );
}

async function open(word: string) {
  const onConfirm = vi.fn();
  const r = renderWithIntl(<Harness onConfirm={onConfirm} word={word} />);
  await r.user.click(screen.getByRole('button', { name: 'Сбросить доступ' }));
  return { ...r, onConfirm };
}

test('кнопка неактивна, пока слово не совпало точно', async () => {
  const { user, onConfirm } = await open('СБРОСИТЬ');
  const dialog = screen.getByRole('dialog', { name: 'Сбросить доступ Марии?' });
  expect(dialog).toHaveAccessibleDescription('Пароль и второй фактор перестанут работать.');
  expect(screen.getByText('Все сеансы завершатся')).toBeInTheDocument();
  const input = screen.getByLabelText('Введите «СБРОСИТЬ», чтобы подтвердить');
  expect(input).toHaveFocus();
  const confirm = screen.getByRole('button', { name: 'Сбросить' });
  expect(confirm).toBeDisabled();

  await user.type(input, 'СБРОС');
  expect(confirm).toBeDisabled();
  await user.clear(input);
  await user.type(input, 'сбросить');
  expect(confirm).toBeDisabled();
  await user.clear(input);
  await user.type(input, 'СБРОСИТЬ!');
  expect(confirm).toBeDisabled();
  await user.type(input, '{Backspace}');
  expect(confirm).toBeEnabled();

  await user.click(confirm);
  expect(onConfirm).toHaveBeenCalledTimes(1);
});

test('подтверждение почтой: Enter не срабатывает до совпадения', async () => {
  const { user, onConfirm } = await open('maria.k@example.com');
  const input = screen.getByLabelText('Введите «maria.k@example.com», чтобы подтвердить');
  await user.type(input, 'maria.k@example.co{Enter}');
  expect(onConfirm).not.toHaveBeenCalled();
  await user.type(input, 'm{Enter}');
  expect(onConfirm).toHaveBeenCalledTimes(1);
});

test('Esc и «Отмена» закрывают без подтверждения; при новом открытии поле пустое', async () => {
  const { user, onConfirm } = await open('СБРОСИТЬ');
  await user.type(screen.getByLabelText(/Введите/), 'СБРОСИТЬ');
  await user.keyboard('{Escape}');
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Сбросить доступ' })).toHaveFocus();

  await user.click(screen.getByRole('button', { name: 'Сбросить доступ' }));
  expect(screen.getByLabelText(/Введите/)).toHaveValue('');
  await user.click(screen.getByRole('button', { name: 'Отмена' }));
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  expect(onConfirm).not.toHaveBeenCalled();
});

test('loading: подтверждение недоступно, повторный клик не уходит', async () => {
  const onConfirm = vi.fn();
  const { user } = renderWithIntl(
    <ConfirmDialog
      open
      onClose={vi.fn()}
      onConfirm={onConfirm}
      title="Выключить?"
      confirmWord="ДА"
      confirmLabel="Выключить"
      loading
    />,
  );
  await user.type(screen.getByLabelText(/Введите/), 'ДА{Enter}');
  expect(screen.getByRole('button', { name: 'Выключить' })).toBeDisabled();
  expect(onConfirm).not.toHaveBeenCalled();
});

// Разрушающее действие над сотрудником (Task 14): поля причины — в самом диалоге, над вводом почты;
// ошибка запроса показывается внутри, диалог не закрывается, набранная почта не теряется.
test('children — над вводом слова; error — текст по коду внутри диалога, набранное цело', async () => {
  function WithError() {
    const [error, setError] = useState<ApiError | null>(null);
    return (
      <ConfirmDialog
        open
        onClose={vi.fn()}
        onConfirm={() => setError(new ApiError({ status: 409, code: 'staff.last_owner' }))}
        title="Выключить?"
        confirmWord="ivan@xn--d1acufc.xn--p1ai"
        confirmLabel="Выключить"
        error={error}
      >
        <label>
          Причина
          <select defaultValue="security">
            <option value="security">Безопасность</option>
          </select>
        </label>
      </ConfirmDialog>
    );
  }
  const { user } = renderWithIntl(<WithError />);
  const dialog = screen.getByRole('dialog', { name: 'Выключить?' });
  const select = within(dialog).getByLabelText('Причина');
  const input = within(dialog).getByLabelText(/Введите/);
  // поле причины стоит раньше ввода почты (порядок фокуса и чтения)
  expect(select.compareDocumentPosition(input) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  expect(within(dialog).queryByRole('alert')).not.toBeInTheDocument();
  await user.type(input, 'ivan@xn--d1acufc.xn--p1ai');
  await user.click(within(dialog).getByRole('button', { name: 'Выключить' }));
  expect(within(dialog).getByRole('alert')).toHaveTextContent(
    'Нельзя оставить платформу без владельца',
  );
  expect(input).toHaveValue('ivan@xn--d1acufc.xn--p1ai');
});
