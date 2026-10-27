import { renderHook, screen, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { useForm } from 'react-hook-form';
import { IntlProvider } from 'use-intl';
import { describe, expect, test, vi } from 'vitest';
import { ApiError } from '../../api/client';
import { messagesFor } from '../../i18n/messages';
import { renderWithIntl } from '../../test/render';
import { Button } from '../Button';
import { useErrorText } from '../errorText';
import {
  CheckboxField,
  Form,
  FormError,
  MultiSelectField,
  PasswordField,
  SelectField,
  TextField,
} from './index';

type Values = {
  email: string;
  name: string;
  password: string;
  role: string;
  city_ids: string[];
  agree: boolean;
};

const ROLES = [
  { value: 'moderator', label: 'Модератор' },
  { value: 'support', label: 'Поддержка' },
];
const CITIES = [
  { value: 'c1', label: 'Ставрополь' },
  { value: 'c2', label: 'Михайловск' },
  { value: 'c3', label: 'Невинномысск' },
];

function Invite(props: {
  onSubmit: (v: Values) => unknown;
  onError?: (e: ApiError) => boolean | void;
  showError?: boolean;
}) {
  const form = useForm<Values>({
    defaultValues: { email: '', name: '', password: '', role: '', city_ids: [], agree: false },
  });
  return (
    <Form form={form} onSubmit={props.onSubmit} onError={props.onError} showError={props.showError}>
      <TextField name="email" label="Почта" type="email" rules={{ required: true }} />
      <TextField name="name" label="Имя" hint="Как в письме" />
      <PasswordField name="password" label="Пароль" />
      <SelectField name="role" label="Роль" options={ROLES} placeholder="Выберите роль" />
      <MultiSelectField name="city_ids" label="Города" options={CITIES} searchable />
      <CheckboxField name="agree" label="Я сохранил коды" />
      {props.showError === false && <FormError />}
      <Button type="submit">Отправить</Button>
    </Form>
  );
}

const reject = (e: ApiError) => vi.fn(() => Promise.reject(e));
const validation = (...fields: [string, string][]) =>
  new ApiError({
    status: 400,
    code: 'validation.failed',
    fields: fields.map(([field, code]) => ({ field, code })),
  });

describe('Form: поля и отправка', () => {
  test('значения всех полей доходят до onSubmit', async () => {
    const onSubmit = vi.fn();
    const { user } = renderWithIntl(<Invite onSubmit={onSubmit} />);
    await user.type(screen.getByLabelText('Почта'), 'maria.k@example.com');
    await user.type(screen.getByLabelText('Имя'), 'Мария К.');
    await user.type(screen.getByLabelText('Пароль'), 'secret-pass');
    await user.selectOptions(screen.getByLabelText('Роль'), 'moderator');
    await user.click(screen.getByRole('checkbox', { name: 'Михайловск' }));
    await user.click(screen.getByRole('checkbox', { name: 'Ставрополь' }));
    await user.click(screen.getByRole('checkbox', { name: 'Я сохранил коды' }));
    await user.click(screen.getByRole('button', { name: 'Отправить' }));
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    expect(onSubmit.mock.calls[0]?.[0]).toEqual({
      email: 'maria.k@example.com',
      name: 'Мария К.',
      password: 'secret-pass',
      role: 'moderator',
      city_ids: ['c2', 'c1'],
      agree: true,
    });
  });

  test('проверка на клиенте: пустое обязательное поле — текст под полем, запроса нет', async () => {
    const onSubmit = vi.fn();
    const { user } = renderWithIntl(<Invite onSubmit={onSubmit} />);
    await user.click(screen.getByRole('button', { name: 'Отправить' }));
    const email = screen.getByLabelText('Почта');
    await waitFor(() => expect(email).toHaveAttribute('aria-invalid', 'true'));
    expect(email).toHaveAccessibleDescription('Заполните поле');
    expect(onSubmit).not.toHaveBeenCalled();
  });

  test('подсказка поля связана с ним', () => {
    renderWithIntl(<Invite onSubmit={vi.fn()} />);
    expect(screen.getByLabelText('Имя')).toHaveAccessibleDescription('Как в письме');
  });

  test('PasswordField: показать и скрыть пароль', async () => {
    const { user } = renderWithIntl(<Invite onSubmit={vi.fn()} />);
    const input = screen.getByLabelText('Пароль');
    expect(input).toHaveAttribute('type', 'password');
    await user.click(screen.getByRole('button', { name: 'Показать пароль' }));
    expect(input).toHaveAttribute('type', 'text');
    await user.click(screen.getByRole('button', { name: 'Скрыть пароль' }));
    expect(input).toHaveAttribute('type', 'password');
  });

  test('MultiSelectField: поиск сужает список, выбранное — чипами с кнопкой «убрать»', async () => {
    const onSubmit = vi.fn();
    const { user } = renderWithIntl(<Invite onSubmit={onSubmit} />);
    await user.type(screen.getByRole('searchbox', { name: 'Города: поиск' }), 'нев');
    expect(screen.queryByRole('checkbox', { name: 'Ставрополь' })).not.toBeInTheDocument();
    await user.click(screen.getByRole('checkbox', { name: 'Невинномысск' }));
    await user.clear(screen.getByRole('searchbox', { name: 'Города: поиск' }));
    await user.click(screen.getByRole('checkbox', { name: 'Ставрополь' }));
    await user.click(screen.getByRole('button', { name: 'Убрать «Невинномысск»' }));
    expect(screen.getByRole('checkbox', { name: 'Невинномысск' })).not.toBeChecked();
    await user.type(screen.getByLabelText('Почта'), 'a@b.c');
    await user.click(screen.getByRole('button', { name: 'Отправить' }));
    await waitFor(() => expect(onSubmit).toHaveBeenCalled());
    expect(onSubmit.mock.calls[0]?.[0]).toMatchObject({ city_ids: ['c1'] });
  });
});

describe('Form: отправка', () => {
  test('Enter в поиске MultiSelectField не отправляет форму', async () => {
    const onSubmit = vi.fn();
    const { user } = renderWithIntl(<Invite onSubmit={onSubmit} />);
    await user.type(screen.getByLabelText('Почта'), 'a@b.c');
    await user.type(screen.getByRole('searchbox', { name: 'Города: поиск' }), 'нев{Enter}');
    expect(onSubmit).not.toHaveBeenCalled();
    expect(screen.getByRole('checkbox', { name: 'Невинномысск' })).toBeInTheDocument();
  });

  test('повторная отправка, пока идёт первая, гасится', async () => {
    let finish!: () => void;
    const onSubmit = vi.fn(() => new Promise<void>((r) => (finish = r)));
    const { user } = renderWithIntl(<Invite onSubmit={onSubmit} />);
    await user.type(screen.getByLabelText('Почта'), 'a@b.c');
    const submit = screen.getByRole('button', { name: 'Отправить' });
    await user.click(submit);
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    await user.click(submit);
    await user.type(screen.getByLabelText('Имя'), '{Enter}');
    expect(onSubmit).toHaveBeenCalledTimes(1);
    finish();
    await waitFor(() => expect(submit).toBeEnabled());
    await user.click(submit);
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(2));
  });
});

describe('Form: ошибки сервера', () => {
  test('validation.failed раскладывается по полям (body.<поле>), общего текста нет', async () => {
    const { user } = renderWithIntl(
      <Invite
        onSubmit={reject(validation(['body.email', 'format'], ['body.city_ids', 'min_items']))}
      />,
    );
    await user.type(screen.getByLabelText('Почта'), 'maria');
    await user.click(screen.getByRole('button', { name: 'Отправить' }));
    const email = screen.getByLabelText('Почта');
    await waitFor(() => expect(email).toHaveAttribute('aria-invalid', 'true'));
    expect(email).toHaveAccessibleDescription('Неверный формат');
    expect(email).toHaveFocus();
    expect(screen.getByRole('group', { name: 'Города' })).toHaveAccessibleDescription(
      'Выберите хотя бы один вариант',
    );
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  test('validation.failed по полю вне формы — общий текст errors.validation.failed', async () => {
    const { user } = renderWithIntl(
      <Invite onSubmit={reject(validation(['query.q', 'minLength']))} />,
    );
    await user.type(screen.getByLabelText('Почта'), 'a@b.c');
    await user.click(screen.getByRole('button', { name: 'Отправить' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Проверьте поля формы');
  });

  test('код с текстом в локалях — его текст', async () => {
    const { user } = renderWithIntl(
      <Invite onSubmit={reject(new ApiError({ status: 409, code: 'staff.email_taken' }))} />,
    );
    await user.type(screen.getByLabelText('Почта'), 'a@b.c');
    await user.click(screen.getByRole('button', { name: 'Отправить' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Сотрудник с такой почтой уже есть');
  });

  test('неизвестный код — общий текст errors.internal', async () => {
    const { user } = renderWithIntl(
      <Invite onSubmit={reject(new ApiError({ status: 409, code: 'future.unknown_code' }))} />,
    );
    await user.type(screen.getByLabelText('Почта'), 'a@b.c');
    await user.click(screen.getByRole('button', { name: 'Отправить' }));
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Что-то пошло не так. Попробуйте ещё раз',
    );
  });

  test('не ApiError (сбой кода, сеть) — errors.internal, а сама ошибка — в console.error', async () => {
    const spy = vi.spyOn(console, 'error').mockImplementation(() => {});
    const bug = new TypeError('Failed to fetch');
    const { user } = renderWithIntl(<Invite onSubmit={() => Promise.reject(bug)} />);
    await user.type(screen.getByLabelText('Почта'), 'a@b.c');
    await user.click(screen.getByRole('button', { name: 'Отправить' }));
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Что-то пошло не так. Попробуйте ещё раз',
    );
    expect(spy).toHaveBeenCalledWith(expect.any(String), bug);
    spy.mockRestore();
  });

  test('onError вернул true — экран показал ошибку сам, FormError молчит', async () => {
    const onError = vi.fn(() => true);
    const e = new ApiError({ status: 401, code: 'staff.mfa_invalid', attemptsLeft: 2 });
    const { user } = renderWithIntl(<Invite onSubmit={reject(e)} onError={onError} />);
    await user.type(screen.getByLabelText('Почта'), 'a@b.c');
    await user.click(screen.getByRole('button', { name: 'Отправить' }));
    await waitFor(() => expect(onError).toHaveBeenCalledWith(e));
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  test('showError=false — экран ставит <FormError /> сам; новая отправка стирает старую ошибку', async () => {
    const onSubmit = vi
      .fn()
      .mockRejectedValueOnce(new ApiError({ status: 409, code: 'staff.email_taken' }))
      .mockResolvedValueOnce(undefined);
    const { user } = renderWithIntl(<Invite onSubmit={onSubmit} showError={false} />);
    await user.type(screen.getByLabelText('Почта'), 'a@b.c');
    await user.click(screen.getByRole('button', { name: 'Отправить' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Сотрудник с такой почтой уже есть');
    await user.click(screen.getByRole('button', { name: 'Отправить' }));
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument());
  });

  test('FormError вне формы — по переданной ошибке', () => {
    const { rerender } = renderWithIntl(<FormError error={null} />);
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    rerender(<FormError error={new ApiError({ status: 403, code: 'auth.forbidden' })} />);
    expect(screen.getByRole('alert')).toHaveTextContent('Недостаточно прав для этого действия');
  });
});

describe('useErrorText', () => {
  const wrapper = ({ children }: { children: ReactNode }) => (
    <IntlProvider locale="ru" messages={messagesFor('ru')}>
      {children}
    </IntlProvider>
  );
  const text = () => renderHook(() => useErrorText(), { wrapper }).result.current;

  test('код → errors.<код>; группа, пустой и неизвестный код → errors.internal', () => {
    const t = text();
    expect(t('staff.mfa_invalid')).toBe('Код не подошёл');
    expect(t(new ApiError({ status: 429, code: 'ratelimit.exceeded' }))).toBe(
      'Слишком много запросов. Подождите немного',
    );
    expect(t('auth')).toBe('Что-то пошло не так. Попробуйте ещё раз');
    expect(t('')).toBe('Что-то пошло не так. Попробуйте ещё раз');
    expect(t('future.unknown_code')).toBe('Что-то пошло не так. Попробуйте ещё раз');
  });
});
