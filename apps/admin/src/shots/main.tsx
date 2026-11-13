import '../app/styles';
import { type ComponentType, StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { createApi } from '../api/client';
import { createQueryClient } from '../app/query';
import { SEARCH_DEBOUNCE_MS } from '../ui/table/model';
import { problem } from '../test/problem';
import { auditServer } from '../test/audit';
import { importsShotsServer } from '../test/geo';
import { sectionsServer } from '../test/sections';
import { applyShotsFixture } from './fixtures';
import { shotsMe } from './params';
import { http, server, start } from './server';
import { ShotsRoot } from './ShotsRoot';

// Точка входа режима shots (спека §5.4, план UF1a.1): та же админка, что src/main.tsx (стили —
// app/styles.ts, App на browser history), но API отвечают моки MSW в браузере на фикстурах тестов.
// Роль — ?shots-role=owner|admin|moderator|setup|anonymous (по умолчанию admin), язык —
// ?shots-locale=ru|en (язык интерфейса — me.locale; у anonymous — язык браузера, его задаёт --locale скрипта). Когда первая
// отрисовка готова, шрифты загружены и запросов нет, на <html> встаёт data-shots-ready — его ждёт
// scripts/admin-shots.mjs. В обычную сборку модуль не попадает (страж src/shots/shots.test.ts).
// ?shots-story=<путь от src без .stories.tsx>:<экспорт> — снимок истории компонента вместо приложения.
// ?shots-fixture=<набор> — данные состояния (fixtures.ts); ?shots-palette=<текст> — после первой отрисовки открыть
// палитру (Ctrl+K — тем же путём, что сотрудник) и ввести текст: результаты, ошибка группы, «Ничего не нашлось».

// Секрет-фикстура экрана настройки 2FA (как в тестах SetupMfaPage) — не настоящий.
const MFA_SECRET = 'JBSWY3DPEHPK3PXP';

const params = new URLSearchParams(location.search);
// неизвестные роль или язык — ошибка до моков (pageerror → скрипт снимков выходит с кодом 1)
const { role: roleName, me: roleMe } = shotsMe(params);

sectionsServer();
const me = applyShotsFixture(params.get('shots-fixture'), roleMe);
// журнал на снимках — с записями фикстуры тестов (LOG): раскрытая запись ?entry=, скрытые по ширине столбцы и
// активная строка видны (sectionsServer отдаёт пустой журнал — тестам маршрутов содержимое не важно)
auditServer();
// «Импорт GeoNames» (G9 Task 20): журнал, задача в очереди и отчёт сверки — только на своём адресе (плитка главной
// и прочие снимки — на моках sectionsServer)
// (с ?shots-fixture=imports-* — данные набора: пусто, ошибка)
if (location.pathname === '/geo/imports' && !params.get('shots-fixture')) importsShotsServer();
server.use(
  http.get('/v1/staff/me', ({ response }) =>
    me ? response(200).json(me) : response.untyped(problem(401, 'auth.unauthenticated')),
  ),
);
if (roleName === 'setup') {
  server.use(
    http.post('/v1/staff/me/mfa/setup', ({ response }) =>
      response(200).json({
        secret: MFA_SECRET,
        otpauth_uri: `otpauth://totp/WF%20admin:olga.v%40wf.example?secret=${MFA_SECRET}&issuer=WF%20admin`,
      }),
    ),
  );
}

const nextFrame = () => new Promise<void>((r) => requestAnimationFrame(() => r()));
const tick = () => new Promise<void>((r) => setTimeout(r, 50));

await start();

// История компонента вместо приложения (до витрины UF3): ?shots-story=ui/icon/Icon:AllIcons —
// путь *.stories.tsx от src без расширения и имя экспорта.
async function loadStory(spec: string): Promise<ComponentType> {
  const stories = import.meta.glob<Record<string, unknown>>('../**/*.stories.tsx');
  const [file, name = 'default'] = spec.split(':');
  const load = stories[`../${file}.stories.tsx`];
  if (!load) throw new Error(`shots: нет истории ${file}.stories.tsx`);
  const story = (await load())[name];
  if (typeof story !== 'function')
    throw new Error(`shots: в ${file}.stories.tsx нет истории ${name}`);
  return story as ComponentType;
}

const storySpec = params.get('shots-story');
const Story = storySpec ? await loadStory(storySpec) : null;

const queryClient = createQueryClient();
const root = document.getElementById('root');
if (!root) throw new Error('нет #root в index.html');
createRoot(root).render(
  <StrictMode>
    <ShotsRoot api={createApi('')} queryClient={queryClient} Story={Story} />
  </StrictMode>,
);

// Ни одного запроса в полёте два кадра подряд (ленивые части и перечитывания успели).
const busy = () => queryClient.isFetching() > 0 || queryClient.isMutating() > 0;
async function settle() {
  for (let quiet = 0; quiet < 2;) {
    await nextFrame();
    await tick();
    quiet = busy() ? 0 : quiet + 1;
  }
}

// Готово: шрифты и тишина; с ?shots-palette= — ещё палитра с введённым текстом и пауза ввода палитры.
await document.fonts.ready;
await settle();
const paletteText = params.get('shots-palette');
if (paletteText !== null) {
  document.body.dispatchEvent(
    new KeyboardEvent('keydown', { code: 'KeyK', key: 'k', ctrlKey: true, bubbles: true }),
  );
  await nextFrame();
  const field = document.querySelector<HTMLInputElement>('[role="combobox"]');
  if (!field) throw new Error('shots: палитра не открылась');
  // контролируемое поле React: значение — через сеттер прототипа, затем событие input
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(
    field,
    paletteText,
  );
  field.dispatchEvent(new Event('input', { bubbles: true }));
  await new Promise((r) => setTimeout(r, SEARCH_DEBOUNCE_MS + 100));
  await settle();
}
document.documentElement.setAttribute('data-shots-ready', '');
