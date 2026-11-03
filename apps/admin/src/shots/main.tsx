import '../app/styles';
import { type ComponentType, StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { App } from '../App';
import { createApi } from '../api/client';
import type { Me } from '../app/permissions';
import { createQueryClient } from '../app/query';
import { meGeoModerator } from '../test/geo';
import { meAdmin, meOwner, meSetup } from '../test/me';
import { problem } from '../test/problem';
import { sectionsServer } from '../test/sections';
import { http, server, start } from './server';

// Точка входа режима shots (спека §5.4, план UF1a.1): та же админка, что src/main.tsx (стили —
// app/styles.ts, App на browser history), но API отвечают моки MSW в браузере на фикстурах тестов.
// Роль — ?shots-role=owner|admin|moderator|setup|anonymous (по умолчанию admin), язык —
// ?shots-locale=ru|en (язык интерфейса — me.locale; у anonymous — язык по умолчанию). Когда первая
// отрисовка готова, шрифты загружены и запросов нет, на <html> встаёт data-shots-ready — его ждёт
// scripts/admin-shots.mjs. В обычную сборку модуль не попадает (страж src/shots/shots.test.ts).
// ?shots-story=<путь от src без .stories.tsx>:<экспорт> — снимок истории компонента вместо приложения.

const ROLES: Record<string, Me | null> = {
  owner: meOwner,
  admin: meAdmin,
  moderator: meGeoModerator,
  setup: meSetup,
  anonymous: null,
};

// Секрет-фикстура экрана настройки 2FA (как в тестах SetupMfaPage) — не настоящий.
const MFA_SECRET = 'JBSWY3DPEHPK3PXP';

const params = new URLSearchParams(location.search);
const roleName = params.get('shots-role') ?? 'admin';
if (!(roleName in ROLES)) throw new Error(`shots: неизвестная роль ${roleName}`);
const locale = params.get('shots-locale');
const base = ROLES[roleName] ?? null;
const me: Me | null = base && locale ? { ...base, locale } : base;

sectionsServer();
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
    {Story ? <Story /> : <App api={createApi('')} queryClient={queryClient} />}
  </StrictMode>,
);

// Готово: шрифты, ни одного запроса в полёте два кадра подряд (ленивые части и перечитывания успели).
await document.fonts.ready;
for (let quiet = 0; quiet < 2;) {
  await nextFrame();
  await tick();
  quiet = queryClient.isFetching() === 0 && queryClient.isMutating() === 0 ? quiet + 1 : 0;
}
document.documentElement.setAttribute('data-shots-ready', '');
