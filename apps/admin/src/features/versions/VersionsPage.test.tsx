import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, test } from 'vitest';
import { renderApp } from '../../test/app';
import {
  appVersions,
  meDirectoriesAdmin,
  meDirectoriesSupport,
  versionsServer,
} from '../../test/geo';
import { problem } from '../../test/problem';
import { compareVersions, raisesMin } from './semver';

// «Версии приложения» (план B, Task 21; спека UI-фундамента §6.2): две карточки (iOS, Android), PUT платформы
// целиком; подъём минимальной — ConfirmDialog (внешнее последствие, m-4), без «Отменить»; ошибки полей — у полей;
// без platform.versions.manage — только чтение; ввод → уход — один вопрос (I-13).

afterEach(() => sessionStorage.clear());

const IOS = appVersions({
  min_version: '1.4.0',
  recommended_version: '1.6.2',
  store_url: 'https://apps.apple.com/app/id6740000000',
  version: 3,
  updated_at: '2026-10-27T15:40:00Z',
});

const card = (name: string) => screen.findByRole('region', { name });
const box = (el: HTMLElement, name: string | RegExp) => within(el).getByRole('textbox', { name });

describe('версии', () => {
  test('две карточки; незаведённая платформа — пустая форма «не задано»', async () => {
    versionsServer({ items: [IOS, appVersions({ platform: 'android' })] });
    renderApp('/app/versions', { me: meDirectoriesAdmin });
    const ios = await card('iOS');
    expect(box(ios, /Минимальная/)).toHaveValue('1.4.0');
    const android = await card('Android');
    expect(android).toHaveTextContent('не задано');
    expect(box(android, /Минимальная/)).toHaveValue('');
    expect(box(android, /магазин/)).toHaveAccessibleDescription(/play\.google\.com/);
  });

  test('сохранение без подъёма минимальной — PUT сразу, тост, без подтверждения', async () => {
    const { sent } = versionsServer({ items: [IOS, appVersions({ platform: 'android' })] });
    renderApp('/app/versions', { me: meDirectoriesAdmin });
    const ios = await card('iOS');
    const user = userEvent.setup();
    await user.clear(box(ios, /Рекомендуемая/));
    await user.type(box(ios, /Рекомендуемая/), '1.7.0');
    await user.click(within(ios).getByRole('button', { name: 'Сохранить' }));
    expect(await screen.findByText('Версии iOS сохранены')).toBeInTheDocument();
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(sent).toEqual([
      {
        body: {
          min_version: '1.4.0',
          recommended_version: '1.7.0',
          store_url: 'https://apps.apple.com/app/id6740000000',
        },
        key: expect.any(String),
        path: '/v1/app/versions/ios',
      },
    ]);
  });

  test('подъём минимальной — подтверждение версией, без него PUT нет', async () => {
    const { sent } = versionsServer({ items: [IOS, appVersions({ platform: 'android' })] });
    renderApp('/app/versions', { me: meDirectoriesAdmin });
    const ios = await card('iOS');
    const user = userEvent.setup();
    await user.clear(box(ios, /Минимальная/));
    await user.type(box(ios, /Минимальная/), '1.5.0');
    await user.click(within(ios).getByRole('button', { name: 'Сохранить' }));
    const dialog = await screen.findByRole('dialog', {
      name: 'Поднять минимальную версию iOS до 1.5.0?',
    });
    expect(dialog).toHaveTextContent('Клиенты ниже 1.5.0 получат требование обновиться');
    expect(sent).toHaveLength(0);
    await user.type(within(dialog).getByRole('textbox'), '1.5.0');
    await user.click(within(dialog).getByRole('button', { name: 'Поднять' }));
    expect(await screen.findByText('Версии iOS сохранены')).toBeInTheDocument();
    expect(sent[0]?.body.min_version).toBe('1.5.0');
  });

  // Ревью G9-C, m-4: без изменений — PUT нет (версия записи и журнал не растут пустой правкой).
  test('«Сохранить» без изменений — запроса нет', async () => {
    const { sent } = versionsServer({ items: [IOS, appVersions({ platform: 'android' })] });
    renderApp('/app/versions', { me: meDirectoriesAdmin });
    const ios = await card('iOS');
    await userEvent.setup().click(within(ios).getByRole('button', { name: 'Сохранить' }));
    expect(await screen.findByText('Изменений нет')).toBeInTheDocument();
    expect(sent).toHaveLength(0);
  });

  // Ревью G9-C, m-3: ошибка прошлого обычного сохранения не показывается в только что открытом окне подъёма.
  test('ошибка прошлого сохранения — не в окне подъёма', async () => {
    let fail = true;
    versionsServer({
      items: [IOS, appVersions({ platform: 'android' })],
      answer: () => (fail ? problem(500, 'internal') : undefined),
    });
    renderApp('/app/versions', { me: meDirectoriesAdmin });
    const ios = await card('iOS');
    const user = userEvent.setup();
    await user.clear(box(ios, /Рекомендуемая/));
    await user.type(box(ios, /Рекомендуемая/), '1.7.0');
    await user.click(within(ios).getByRole('button', { name: 'Сохранить' }));
    await within(ios).findByRole('alert');
    fail = false;
    await user.clear(box(ios, /Минимальная/));
    await user.type(box(ios, /Минимальная/), '1.5.0');
    await user.click(within(ios).getByRole('button', { name: 'Сохранить' }));
    const dialog = await screen.findByRole('dialog', {
      name: 'Поднять минимальную версию iOS до 1.5.0?',
    });
    expect(within(dialog).queryByRole('alert')).toBeNull();
  });

  test('400 validation.failed — ошибки min_version и store_url у полей', async () => {
    versionsServer({
      items: [IOS, appVersions({ platform: 'android' })],
      answer: () =>
        problem(400, 'validation.failed', {
          errors: [
            { field: 'body.recommended_version', code: 'minimum' },
            { field: 'body.store_url', code: 'host' },
          ],
        }),
    });
    renderApp('/app/versions', { me: meDirectoriesAdmin });
    const ios = await card('iOS');
    const user = userEvent.setup();
    await user.clear(box(ios, /магазин/));
    await user.type(box(ios, /магазин/), 'https://example.com/app');
    await user.click(within(ios).getByRole('button', { name: 'Сохранить' }));
    await waitFor(() =>
      expect(box(ios, /магазин/)).toHaveAccessibleDescription(/не на магазин этой платформы/),
    );
    expect(box(ios, /Рекомендуемая/)).toHaveAccessibleDescription(/меньше допустимого/);
  });

  test('формат до запроса: не MAJOR.MINOR.PATCH и рекомендуемая ниже минимальной', async () => {
    const { sent } = versionsServer({ items: [IOS, appVersions({ platform: 'android' })] });
    renderApp('/app/versions', { me: meDirectoriesAdmin });
    const ios = await card('iOS');
    const user = userEvent.setup();
    await user.clear(box(ios, /Рекомендуемая/));
    await user.type(box(ios, /Рекомендуемая/), '1.3.9');
    await user.click(within(ios).getByRole('button', { name: 'Сохранить' }));
    await waitFor(() =>
      expect(box(ios, /Рекомендуемая/)).toHaveAccessibleDescription(/меньше допустимого/),
    );
    await user.clear(box(ios, /Минимальная/));
    await user.type(box(ios, /Минимальная/), '01.4');
    await user.click(within(ios).getByRole('button', { name: 'Сохранить' }));
    await waitFor(() =>
      expect(box(ios, /Минимальная/)).toHaveAccessibleDescription(/Неверный формат/),
    );
    expect(sent).toHaveLength(0);
  });

  test('поддержка — только чтение: поля без правки, без «Сохранить»', async () => {
    versionsServer({ items: [IOS, appVersions({ platform: 'android' })] });
    renderApp('/app/versions', { me: meDirectoriesSupport });
    const ios = await card('iOS');
    expect(box(ios, /Минимальная/)).toHaveAttribute('readonly');
    expect(within(ios).queryByRole('button', { name: 'Сохранить' })).toBeNull();
  });

  test('I-13: ввод → пункт меню — ровно один вопрос; «Остаться» — ввод цел', async () => {
    versionsServer({ items: [IOS, appVersions({ platform: 'android' })] });
    const app = renderApp('/app/versions', { me: meDirectoriesAdmin, history: 'browser' });
    const ios = await card('iOS');
    const user = userEvent.setup();
    await user.type(box(ios, /Рекомендуемая/), '1');
    await user.click(screen.getByRole('link', { name: 'Страны' }));
    await waitFor(() =>
      expect(screen.getAllByRole('dialog', { name: 'Уйти без сохранения?' })).toHaveLength(1),
    );
    await user.click(screen.getByRole('button', { name: 'Остаться' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(app.at()).toBe('/app/versions');
    expect(box(ios, /Рекомендуемая/)).toHaveValue('1.6.21');
  });
});

describe('сравнение версий', () => {
  test('числа, а не строки; первая запись минимальной — подъём', () => {
    expect(compareVersions('1.10.0', '1.9.9')).toBeGreaterThan(0);
    expect(compareVersions('1.4.0', '1.4.0')).toBe(0);
    expect(raisesMin(null, '1.0.0')).toBe(true);
    expect(raisesMin('1.4.0', '1.4.0')).toBe(false);
    expect(raisesMin('1.4.0', '1.3.0')).toBe(false);
    expect(raisesMin('1.9.0', '1.10.0')).toBe(true);
  });
});
