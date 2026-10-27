import { screen, waitFor, within } from '@testing-library/react';
import userEvent, { type UserEvent } from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { reauthBus } from '../../app/reauth';
import { renderApp, storageDump } from '../../test/app';
import { meOwner, meSetup } from '../../test/me';
import { problem } from '../../test/problem';
import { http, server } from '../../test/server';
import { sectionsServer } from '../../test/sections';

// Настройка 2FA (спека §9.4 п. 3, §4.5): секрет выпускается один раз (и в StrictMode), QR рисуется
// на клиенте, код → 10 резервных кодов → «Я сохранил коды» → «Готово» (ack) → стартовая. Секрет и
// коды не попадают ни в storage, ни в кэш запросов после ухода с экрана.

// стартовая и возврат приводят в разделы каркаса — их первые запросы описаны (src/test/sections.ts)
beforeEach(() => sectionsServer());

afterEach(() => {
  reauthBus.reject();
  sessionStorage.clear();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

const SECRET = 'JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP';
const CODES = [
  'k7m2q-x9p4t',
  'a3b5c-d7e2f',
  'm4n6p-q2r8s',
  't5v7w-x3y9z',
  'b2c4d-f6g8h',
  'j3k5m-n7p2q',
  'r4s6t-v8w3x',
  'y5z7a-b2c9d',
  'e3f5g-h7j4k',
  'p6q8r-s2t5v',
];
const CODE = 'Код из приложения-аутентификатора';
const page = (name: string) => screen.findByRole('heading', { level: 1, name });

type Ctl = { setup: (string | null)[]; confirm: number; ack: number };

// Сервер настройки: setup выдаёт секрет, confirm с 492817 — коды, ack — стадия active.
function setupServer(
  app: ReturnType<typeof renderApp>,
  over: { setup?: () => Response; ack?: () => Response } = {},
) {
  const ctl: Ctl = { setup: [], confirm: 0, ack: 0 };
  server.use(
    http.post('/v1/staff/me/mfa/setup', ({ request, response }) => {
      ctl.setup.push(request.headers.get('Idempotency-Key'));
      if (over.setup) return response.untyped(over.setup());
      return response(200).json({
        secret: SECRET,
        otpauth_uri: `otpauth://totp/WF%20admin:olga.v%40wf.example?secret=${SECRET}&issuer=WF%20admin`,
      });
    }),
    http.post('/v1/staff/me/mfa/confirm', async ({ request, response }) => {
      ctl.confirm++;
      const { code } = await request.json();
      if (code !== '492817') return response.untyped(problem(400, 'staff.mfa_invalid'));
      return response(200).json({ recovery_codes: CODES });
    }),
    http.post('/v1/staff/me/mfa/recovery-codes/ack', ({ response }) => {
      ctl.ack++;
      if (over.ack) return response.untyped(over.ack());
      app.me.current = meOwner;
      return response(204).empty();
    }),
  );
  return ctl;
}

async function enterCode(user: UserEvent, code: string) {
  const group = await screen.findByRole('group', { name: CODE });
  await user.click(within(group).getAllByRole('textbox')[0]!);
  await user.keyboard(code);
}

describe('настройка 2FA', () => {
  test('секрет выпускается один раз и в StrictMode; QR (SVG) и секрет текстом', async () => {
    // запросы считаются в момент отправки: fetch зовётся синхронно с выпуском, задолго до ответа
    const fetchSpy = vi.spyOn(globalThis, 'fetch');
    const sentSetup = () =>
      fetchSpy.mock.calls.filter(([r]) => r instanceof Request && r.url.includes('/mfa/setup'))
        .length;
    const app = renderApp('/setup-2fa', { me: meSetup, strict: true });
    const ctl = setupServer(app);
    await page('Настройка двухфакторной защиты');
    const qr = await screen.findByRole('img', { name: 'QR-код для приложения-аутентификатора' });
    expect(qr.tagName.toLowerCase()).toBe('svg');
    expect(qr.querySelector('path')?.getAttribute('d')).toMatch(/^M\d/);
    expect(screen.getByText('JBSW Y3DP EHPK 3PXP JBSW Y3DP EHPK 3PXP')).toBeInTheDocument();
    // все отправленные выпуски дошли до сервера — и он один
    await waitFor(() => expect(ctl.setup).toHaveLength(sentSetup()));
    expect(sentSetup()).toBe(1);
    expect(ctl.setup).toHaveLength(1);
    expect(ctl.setup[0]).toBeTruthy();
  });

  test('«Скопировать» кладёт секрет в буфер без пробелов', async () => {
    const app = renderApp('/setup-2fa', { me: meSetup });
    setupServer(app);
    const user = userEvent.setup();
    await screen.findByRole('img', { name: 'QR-код для приложения-аутентификатора' });
    await user.click(screen.getByRole('button', { name: 'Скопировать секрет' }));
    expect(await navigator.clipboard.readText()).toBe(SECRET);
    expect(await screen.findByText('Скопировано')).toBeInTheDocument();
  });

  test('код → 10 резервных кодов; «Готово» недоступна без флажка; ack → стартовая', async () => {
    const app = renderApp('/setup-2fa', { me: meSetup });
    const ctl = setupServer(app);
    const user = userEvent.setup();
    await enterCode(user, '492817');
    const list = await screen.findByRole('list', { name: 'Резервные коды' });
    expect(
      within(list)
        .getAllByRole('listitem')
        .map((li) => li.textContent),
    ).toEqual(CODES);
    // секрет и QR больше не на экране
    expect(screen.queryByText(/JBSW Y3DP/)).not.toBeInTheDocument();
    expect(screen.queryByRole('img', { name: 'QR-код для приложения-аутентификатора' })).toBeNull();

    const done = screen.getByRole('button', { name: 'Готово' });
    expect(done).toBeDisabled();
    await user.click(done);
    expect(ctl.ack).toBe(0);
    await user.click(screen.getByLabelText('Я сохранил коды'));
    expect(done).toBeEnabled();

    // ни секрета, ни кодов в storage и в кэше запросов
    expect(storageDump()).not.toContain(SECRET);
    for (const c of CODES) expect(storageDump()).not.toContain(c);
    await waitFor(() =>
      expect(
        JSON.stringify(
          app.queryClient
            .getQueryCache()
            .getAll()
            .map((q) => q.state.data),
        ),
      ).not.toContain(SECRET),
    );

    await user.click(done);
    await page('Журнал действий');
    expect(app.at()).toBe('/audit');
    expect(ctl.ack).toBe(1);
    expect(ctl.setup).toHaveLength(1);
    // после ухода с экрана в кэше нет ни секрета, ни кодов (запросы и мутации)
    const cached = JSON.stringify([
      app.queryClient
        .getQueryCache()
        .getAll()
        .map((q) => q.state.data),
      app.queryClient
        .getMutationCache()
        .getAll()
        .map((m) => m.state.data),
    ]);
    expect(cached).not.toContain(SECRET);
    expect(cached).not.toContain(CODES[0]);
  });

  test('«Скопировать» и «Скачать .txt» отдают все 10 кодов', async () => {
    const app = renderApp('/setup-2fa', { me: meSetup });
    setupServer(app);
    const user = userEvent.setup();
    const blobs: Blob[] = [];
    vi.stubGlobal(
      'URL',
      class extends URL {
        static createObjectURL = (b: Blob | MediaSource) => {
          if (b instanceof Blob) blobs.push(b);
          return 'blob:codes';
        };
        static revokeObjectURL = () => {};
      },
    );
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});
    await enterCode(user, '492817');
    await screen.findByRole('list', { name: 'Резервные коды' });

    await user.click(screen.getByRole('button', { name: 'Скопировать коды' }));
    expect(await navigator.clipboard.readText()).toBe(CODES.join('\n'));

    await user.click(screen.getByRole('button', { name: 'Скачать .txt' }));
    expect(click).toHaveBeenCalledTimes(1);
    const a = click.mock.contexts[0] as HTMLAnchorElement;
    expect(a.download).toMatch(/\.txt$/);
    const text = await blobs[0]!.text();
    for (const c of CODES) expect(text).toContain(c);
  });

  // Ревью M4: на время confirm ячейки busy (readOnly), а не disabled — фокус не падает на body.
  test('пока confirm идёт, ячейки включены и заняты (aria-busy)', async () => {
    const app = renderApp('/setup-2fa', { me: meSetup });
    setupServer(app);
    let release: () => void = () => {};
    const gate = new Promise<void>((r) => (release = r));
    server.use(
      http.post('/v1/staff/me/mfa/confirm', async ({ response }) => {
        await gate;
        return response(200).json({ recovery_codes: CODES });
      }),
    );
    const user = userEvent.setup();
    await enterCode(user, '492817');
    const group = screen.getByRole('group', { name: CODE });
    await waitFor(() => expect(group).toHaveAttribute('aria-busy', 'true'));
    for (const cell of within(group).getAllByRole('textbox')) expect(cell).toBeEnabled();
    release();
    expect(await screen.findByRole('list', { name: 'Резервные коды' })).toBeInTheDocument();
  });

  test('код не подошёл — текст и ячейки очищены, стёртый код не уходит', async () => {
    const app = renderApp('/setup-2fa', { me: meSetup });
    const ctl = setupServer(app);
    const user = userEvent.setup();
    await enterCode(user, '111111');
    expect(await screen.findByRole('alert')).toHaveTextContent('Код не подошёл');
    const cells = within(screen.getByRole('group', { name: CODE })).getAllByRole('textbox');
    await waitFor(() => expect(cells.map((c) => (c as HTMLInputElement).value).join('')).toBe(''));
    // стёртый код не уходит повторно
    const confirm = screen.getByRole('button', { name: 'Подтвердить' });
    expect(confirm).toBeDisabled();
    await user.click(confirm);
    expect(ctl.confirm).toBe(1);
  });

  test('«Выйти» на экране настройки — выход без return (брошенная настройка на общем компьютере)', async () => {
    const app = renderApp('/setup-2fa', { me: meSetup });
    setupServer(app);
    let out = 0;
    server.use(
      http.post('/v1/staff/auth/logout', ({ response }) => {
        out++;
        app.me.current = null;
        return response(204).empty();
      }),
    );
    const user = userEvent.setup();
    await screen.findByRole('img', { name: 'QR-код для приложения-аутентификатора' });
    await user.click(screen.getByRole('button', { name: 'Выйти' }));
    await page('Вход администратора');
    await waitFor(() => expect(app.at()).toBe('/login'));
    expect(out).toBe(1);
  });
});

describe('перезагрузки и повторы', () => {
  test('setup → 409 staff.wrong_stage — «уже подтверждён», «Продолжить» → ack → стартовая', async () => {
    const app = renderApp('/setup-2fa', { me: meSetup });
    const ctl = setupServer(app, { setup: () => problem(409, 'staff.wrong_stage') });
    const user = userEvent.setup();
    expect(await screen.findByText('Второй фактор уже подтверждён')).toBeInTheDocument();
    expect(screen.queryByRole('group', { name: CODE })).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Продолжить' }));
    await page('Журнал действий');
    expect(ctl.ack).toBe(1);
  });

  test('ack → 409 wrong_stage, а me всё ещё setup (секрет не выпущен: повторный accept) — снова сканирование', async () => {
    const app = renderApp('/setup-2fa', { me: meSetup });
    const ctl = setupServer(app, { ack: () => problem(409, 'staff.wrong_stage') });
    server.use(
      http.post('/v1/staff/me/mfa/confirm', ({ response }) =>
        response.untyped(problem(409, 'staff.wrong_stage')),
      ),
    );
    const user = userEvent.setup();
    await enterCode(user, '492817');
    await user.click(await screen.findByRole('button', { name: 'Продолжить' }));
    // новый секрет — снова QR и ячейки
    expect(
      await screen.findByRole('img', { name: 'QR-код для приложения-аутентификатора' }),
    ).toBeInTheDocument();
    expect(ctl.setup).toHaveLength(2);
    expect(ctl.ack).toBe(1);
  });

  test('ack и setup оба отвечают wrong_stage — без петли: второй раз ошибка, а не новое сканирование', async () => {
    const app = renderApp('/setup-2fa', { me: meSetup });
    const ctl = setupServer(app, {
      setup: () => problem(409, 'staff.wrong_stage'),
      ack: () => problem(409, 'staff.wrong_stage'),
    });
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: 'Продолжить' }));
    await waitFor(() => expect(ctl.setup).toHaveLength(2));
    await user.click(await screen.findByRole('button', { name: 'Продолжить' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Это действие сейчас недоступно');
    expect(ctl.ack).toBe(2);
    expect(ctl.setup).toHaveLength(2);
  });

  test('ack → 409 wrong_stage, а настройка уже завершена (другая вкладка) — стартовая', async () => {
    const app = renderApp('/setup-2fa', { me: meSetup });
    const ctl = setupServer(app, {
      setup: () => problem(409, 'staff.wrong_stage'),
      ack: () => {
        app.me.current = meOwner;
        return problem(409, 'staff.wrong_stage');
      },
    });
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: 'Продолжить' }));
    await page('Журнал действий');
    expect(ctl.ack).toBe(1);
  });

  test('confirm → idempotency.replay_unavailable — понятный текст и «Начать заново» (новый секрет)', async () => {
    const app = renderApp('/setup-2fa', { me: meSetup });
    const ctl = setupServer(app);
    server.use(
      http.post('/v1/staff/me/mfa/confirm', ({ response }) =>
        response.untyped(problem(409, 'idempotency.replay_unavailable')),
      ),
    );
    const user = userEvent.setup();
    await enterCode(user, '492817');
    expect(await screen.findByRole('alert')).toHaveTextContent('Ответ с секретом не сохраняется');
    await user.click(screen.getByRole('button', { name: 'Начать заново' }));
    await waitFor(() => expect(ctl.setup).toHaveLength(2));
    expect(ctl.setup[1]).not.toBe(ctl.setup[0]);
    expect(await screen.findByRole('group', { name: CODE })).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  test('setup → idempotency.replay_unavailable — «Начать заново» выпускает секрет снова', async () => {
    const app = renderApp('/setup-2fa', { me: meSetup });
    let fail = true;
    const ctl = setupServer(app, {
      setup: () =>
        fail
          ? problem(409, 'idempotency.replay_unavailable')
          : (Response.json({ secret: SECRET, otpauth_uri: 'otpauth://totp/x' }) as Response),
    });
    const user = userEvent.setup();
    expect(await screen.findByRole('alert')).toHaveTextContent('Ответ с секретом не сохраняется');
    fail = false;
    await user.click(screen.getByRole('button', { name: 'Начать заново' }));
    expect(await screen.findByText('JBSW Y3DP EHPK 3PXP JBSW Y3DP EHPK 3PXP')).toBeInTheDocument();
    expect(ctl.setup).toHaveLength(2);
  });

  test('на телефоне — тот же экран: QR, секрет и ввод кода', async () => {
    vi.stubGlobal(
      'matchMedia',
      vi.fn((query: string) => ({
        matches: true,
        media: query,
        addEventListener: () => {},
        removeEventListener: () => {},
      })),
    );
    const app = renderApp('/setup-2fa', { me: meSetup });
    setupServer(app);
    expect(
      await screen.findByRole('img', { name: 'QR-код для приложения-аутентификатора' }),
    ).toBeInTheDocument();
    expect(screen.getByRole('group', { name: CODE })).toBeInTheDocument();
  });
});
