import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { HttpResponse } from 'msw';
import { afterEach, describe, expect, test } from 'vitest';
import { renderApp } from '../../test/app';
import { flagsServer } from '../../test/flags';
import { flag, meDirectoriesAdmin } from '../../test/geo';
import { CITY_OTHER, CITY_OWN } from '../../test/me';
import { problem } from '../../test/problem';
import { sectionsServer } from '../../test/sections';
import { server, http } from '../../test/server';

// Матрица «функция × город» (план B, Task 22; спека UI-фундамента §6.2): переключение — PATCH с version флага и
// ПОЛНЫМ списком его городов (не видимыми столбцами), не оптимистично, в полёте клетка busy и остальные клетки
// флага заблокированы; 409 flag_stale — перечитать без повтора; «Отменить» — по ответу прямого (§4.10).

afterEach(() => sessionStorage.clear());

const sw = (name: string) => screen.findByRole('switch', { name });
const checked = (el: HTMLElement) => el.getAttribute('aria-checked');

function setup(flags = [flag()], path = '/flags') {
  sectionsServer();
  const srv = flagsServer(flags);
  const app = renderApp(path, { me: meDirectoriesAdmin });
  return { ...srv, app, user: userEvent.setup() };
}

describe('переключение', () => {
  test('город вне столбцов (поиск сузил) остаётся в теле: список флага ± город', async () => {
    const { calls, user } = setup(
      [flag({ key: 'economy', enabled_city_ids: [CITY_OTHER], version: 4 })],
      '/flags?q=Став',
    );
    const cell = await sw('Экономика: Ставрополь');
    expect(screen.queryByRole('switch', { name: 'Экономика: Пятигорск' })).toBeNull();
    await user.click(cell);
    await waitFor(() => expect(checked(cell)).toBe('true'));
    expect(calls).toHaveLength(1);
    expect(calls[0]?.flag).toBe('economy');
    expect(calls[0]?.body).toEqual({ version: 4, enabled_city_ids: [CITY_OTHER, CITY_OWN] });
  });

  test('«Везде» — enabled_globally с версией флага', async () => {
    const { calls, user } = setup();
    const all = await sw('Подписки: везде');
    await user.click(all);
    await waitFor(() => expect(checked(all)).toBe('true'));
    expect(calls[0]?.body).toEqual({ version: 1, enabled_globally: true });
    expect(await screen.findByText('«Подписки» включено везде')).toBeInTheDocument();
  });

  test('в полёте: клетка busy, остальные клетки флага заблокированы, второго запроса нет', async () => {
    const { calls, answers, user } = setup([flag(), flag({ key: 'economy' })]);
    let release = () => {};
    answers.push(() => new Promise<undefined>((r) => (release = () => r(undefined))));
    const cell = await sw('Подписки: Ставрополь');
    await user.click(cell);
    await waitFor(() => expect(cell).toHaveAttribute('aria-busy', 'true'));
    expect(checked(cell)).toBe('false');
    expect(screen.getByRole('switch', { name: 'Подписки: Пятигорск' })).toBeDisabled();
    expect(screen.getByRole('switch', { name: 'Подписки: везде' })).toBeDisabled();
    // другой флаг — своя версия, не заблокирован
    expect(screen.getByRole('switch', { name: 'Экономика: Ставрополь' })).toBeEnabled();
    await user.click(cell);
    expect(calls).toHaveLength(1);
    release();
    await waitFor(() => expect(checked(cell)).toBe('true'));
    expect(cell).not.toHaveAttribute('aria-busy');
    expect(screen.getByRole('switch', { name: 'Подписки: Пятигорск' })).toBeEnabled();
  });

  test('отказ — клетка прежняя, ошибка тостом', async () => {
    const { answers, user } = setup();
    answers.push(() => problem(403, 'auth.forbidden'));
    const cell = await sw('Подписки: Ставрополь');
    await user.click(cell);
    expect(await screen.findByText('Недостаточно прав для этого действия')).toBeInTheDocument();
    expect(checked(cell)).toBe('false');
  });

  test('обрыв — повтор того же переключения тем же ключом', async () => {
    const { calls, answers, user } = setup();
    answers.push(() => HttpResponse.error());
    const cell = await sw('Подписки: Ставрополь');
    await user.click(cell);
    await waitFor(() => expect(cell).not.toHaveAttribute('aria-busy'));
    expect(checked(cell)).toBe('false');
    await user.click(cell);
    await waitFor(() => expect(checked(cell)).toBe('true'));
    expect(calls).toHaveLength(2);
    expect(calls[1]?.key).toBe(calls[0]?.key);
  });

  test('409 flag_stale — тост, флаги перечитаны, повтора нет', async () => {
    const { calls, state, user } = setup();
    const cell = await sw('Подписки: Ставрополь');
    // другая вкладка включила флаг везде
    state.flags = [flag({ enabled_globally: true, version: 2 })];
    const reads = state.reads;
    await user.click(cell);
    expect(await screen.findByText('Флаг изменили — данные обновлены')).toBeInTheDocument();
    await waitFor(() => expect(checked(cell)).toBe('true'));
    expect(cell).toBeDisabled();
    expect(state.reads).toBeGreaterThan(reads);
    expect(calls).toHaveLength(1);
  });

  test('422 flag_city_not_found — тост, города перечитаны', async () => {
    const seen: string[] = [];
    const { answers, user } = setup();
    server.use(
      http.get('/v1/geo/cities', ({ request }) => {
        seen.push(request.url);
        return undefined;
      }),
    );
    answers.push(() => problem(422, 'platform.flag_city_not_found'));
    const cell = await sw('Подписки: Ставрополь');
    const before = seen.length;
    await user.click(cell);
    expect(
      await screen.findByText('Среди выбранных городов есть несуществующий'),
    ).toBeInTheDocument();
    await waitFor(() => expect(seen.length).toBeGreaterThan(before));
    expect(checked(cell)).toBe('false');
  });
});

describe('«Отменить» (§4.10)', () => {
  test('обратное — версия и список из ответа прямого ∓ город, новым ключом', async () => {
    const { calls, answers, user } = setup();
    // ответ прямого: сервер успел добавить чужой город и поднять версию сильнее
    answers.push(() =>
      HttpResponse.json(flag({ enabled_city_ids: [CITY_OWN, CITY_OTHER], version: 9 }), {
        status: 200,
      }),
    );
    answers.push(() =>
      HttpResponse.json(flag({ enabled_city_ids: [CITY_OTHER], version: 10 }), { status: 200 }),
    );
    const cell = await sw('Подписки: Ставрополь');
    await user.click(cell);
    await screen.findByText('«Подписки» включено: Ставрополь');
    await user.click(screen.getByRole('button', { name: 'Отменить' }));
    expect(await screen.findByText('Отменено')).toBeInTheDocument();
    expect(calls).toHaveLength(2);
    expect(calls[1]?.body).toEqual({ version: 9, enabled_city_ids: [CITY_OTHER] });
    expect(calls[1]?.key).not.toBe(calls[0]?.key);
    await waitFor(() => expect(checked(cell)).toBe('false'));
  });
});

describe('ограничения клеток', () => {
  test('«Везде» включён — городские клетки неактивны, подпись «включено везде»', async () => {
    setup([flag({ key: 'predictions', enabled_globally: true })]);
    const cell = await sw('Прогнозы: Ставрополь');
    expect(cell).toBeDisabled();
    expect(checked(cell)).toBe('true');
    expect(screen.getByRole('rowheader', { name: /Прогнозы/ })).toHaveTextContent('включено везде');
    expect(screen.getByRole('switch', { name: 'Прогнозы: везде' })).toBeEnabled();
  });

  test('1000 городов во флаге — включить следующий нельзя, подпись предела; выключить — можно', async () => {
    const ids = Array.from(
      { length: 999 },
      (_, i) => `0192f1c4-0000-7000-8000-${String(i).padStart(12, '0')}`,
    );
    setup([flag({ enabled_city_ids: [...ids, CITY_OTHER].sort() })]);
    const cell = await sw('Подписки: Ставрополь');
    expect(cell).toBeDisabled();
    expect(screen.getByRole('switch', { name: 'Подписки: Пятигорск' })).toBeEnabled();
    expect(screen.getByRole('rowheader', { name: /Подписки/ })).toHaveTextContent(
      'больше 1000 городов — включите везде',
    );
  });

  test('400 на включение города — тот же текст предела', async () => {
    const { answers, user } = setup();
    answers.push(() =>
      problem(400, 'validation.failed', {
        errors: [{ field: 'body.enabled_city_ids', code: 'max_items' }],
      }),
    );
    await user.click(await sw('Подписки: Ставрополь'));
    expect(await screen.findByText('больше 1000 городов — включите везде')).toBeInTheDocument();
  });
});
