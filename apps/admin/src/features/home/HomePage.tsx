import { useRouter } from '@tanstack/react-router';
import { type ReactNode, useId } from 'react';
import { useTranslations } from 'use-intl';
import { isDesktopOnlyDecision } from '../../app/desktopOnly';
import { paths } from '../../app/paths';
import { useCan } from '../../app/session';
import {
  attentionState,
  AttentionTile,
  AttentionTiles,
  type ChipTone,
  PageHeader,
  RelativeTime,
  Skeleton,
  type TodoItem,
  TodoList,
  useIsHandheld,
  useNow,
} from '../../ui';
import { useIncomingApprovals } from '../approvals/queries';
import styles from './HomePage.module.css';
import {
  CODES_WARN,
  importAlerts,
  useMfaStatus,
  useMyPendingApprovals,
  useRecentImports,
  useWaitlistCities,
} from './queries';

type Query = {
  data: unknown;
  isPending: boolean;
  isError: boolean;
  isFetching: boolean;
  refetch: () => unknown;
};
type Page = { items: unknown[]; next_cursor: string | null };

// Число и «больше» из страницы: у API нет общего счёта, «99+» — по ≥ 100 строк или next_cursor (m7).
const count = (page: Page | undefined) =>
  page
    ? { value: page.items.length, more: page.next_cursor !== null }
    : { value: null, more: false };

// Главная «Что требует внимания» (спека UI-фундамента §4.13): плитки в фиксированном порядке (место не прыгает
// между заходами) — входящие подтверждения, мои запросы, сбои импорта GeoNames, города в ожидании; без права плитки
// нет и запроса нет (enabled). Под плитками — список дел из тех же данных и личное (резервные коды ≤ 3); пусто —
// «Всё спокойно». Ошибка плитки — в ней самой; список дел без упавших плиток строится из остальных, а «Всё
// спокойно» при ошибке не говорится (неизвестно). Ссылки — через объект search роутера (адрес — buildLocation).
export function HomePage() {
  const t = useTranslations('home');
  const tNav = useTranslations('nav');
  const router = useRouter();
  const todoId = useId();
  const handheld = useIsHandheld();
  const now = useNow('minute').getTime();
  const canImports = useCan('geo.imports.read');
  const canCities = useCan('geo.cities.read');

  const incoming = useIncomingApprovals();
  const mine = useMyPendingApprovals();
  const imports = useRecentImports(canImports);
  const waitlist = useWaitlistCities(canCities);
  const mfa = useMfaStatus();

  const link = (to: string, search?: Record<string, unknown>) => ({
    href: router.buildLocation({ to, search }).href,
    onNavigate: () => void router.navigate({ to, search }),
    onIntent: () => void router.preloadRoute({ to, search }).catch(() => undefined),
  });
  const tile = (q: Query) => ({
    state: attentionState(q),
    onRetry: () => void q.refetch(),
    retrying: q.isError && q.isFetching,
  });
  const rich = (key: string, at: string, values: Record<string, string> = {}): ReactNode =>
    t.rich(key, { ...values, time: () => <RelativeTime value={at} /> });

  const todo: TodoItem[] = [];
  const add = (id: string, text: string, tone: ChipTone, to: ReturnType<typeof link>) =>
    todo.push({ id, text, tone, href: to.href, onNavigate: to.onNavigate });

  // входящие: на карманном устройстве решения staff.* — только с компьютера
  const inc = count(incoming.data);
  const incomingDesktop =
    handheld && (incoming.data?.items.some((a) => isDesktopOnlyDecision(a.action)) ?? false);
  if (inc.value) {
    add(
      'incoming',
      inc.more || inc.value > 99
        ? t('todo.incoming_many')
        : t('todo.incoming', { count: inc.value }),
      'warning',
      link(paths.approvals),
    );
  }

  // мои: ближайший срок — RelativeTime
  const my = count(mine.data);
  const nearest = mine.data?.items.reduce<string | undefined>(
    (min, a) =>
      min === undefined || Date.parse(a.expires_at) < Date.parse(min) ? a.expires_at : min,
    undefined,
  );
  if (my.value) {
    add(
      'mine',
      my.more || my.value > 99 ? t('todo.mine_many') : t('todo.mine', { count: my.value }),
      'accent',
      link(paths.approvals, { box: 'mine' }),
    );
  }

  // импорт: по последним 5 записям журнала — последний по стране упал или идёт дольше 30 мин
  const alerts = imports.data ? importAlerts(imports.data.items, now) : [];
  for (const a of alerts) {
    add(
      `import-${a.country}`,
      t(a.kind === 'failed' ? 'todo.import_failed' : 'todo.import_stuck', { country: a.country }),
      'danger',
      link(paths.imports, { country: a.country }),
    );
  }
  const firstAlert = alerts[0];

  const cities = count(waitlist.data);
  if (cities.value)
    add('waitlist', t('todo.waitlist'), 'neutral', link(paths.cities, { status: ['waitlist'] }));

  const codes = mfa.data?.confirmed_at ? mfa.data.recovery_codes_left : undefined;
  if (codes !== undefined && codes <= CODES_WARN) {
    add('codes', t('todo.codes', { count: codes }), 'warning', link(paths.me));
  }

  const shown: Query[] = [incoming, mine, mfa];
  if (canImports) shown.push(imports);
  if (canCities) shown.push(waitlist);
  const loading = shown.some((q) => q.isPending);
  // упавшие источники (и без своей плитки — резервные коды): строка «Не загрузилось · Повторить» в списке дел
  const failed = shown.filter((q) => q.isError);
  const todoError =
    failed.length > 0
      ? {
          onRetry: () => failed.forEach((q) => void q.refetch()),
          retrying: failed.some((q) => q.isFetching),
        }
      : undefined;

  return (
    <>
      <PageHeader title={tNav('home')} subtitle={t('subtitle')} />
      <div className={styles.page}>
        <AttentionTiles label={t('tiles_label')}>
          <li>
            <AttentionTile
              {...link(paths.approvals)}
              {...tile(incoming)}
              title={t('incoming.title')}
              value={inc.value}
              more={inc.more}
              tone="warning"
              note={incomingDesktop && inc.value ? t('incoming.desktop') : undefined}
            />
          </li>
          <li>
            <AttentionTile
              {...link(paths.approvals, { box: 'mine' })}
              {...tile(mine)}
              title={t('mine.title')}
              value={my.value}
              more={my.more}
              tone="accent"
              note={nearest && my.value ? rich('mine.nearest', nearest) : undefined}
            />
          </li>
          {canImports && (
            <li>
              <AttentionTile
                {...link(paths.imports, firstAlert ? { country: firstAlert.country } : undefined)}
                {...tile(imports)}
                title={t('imports.title')}
                value={imports.data ? alerts.length : null}
                tone="danger"
                note={
                  firstAlert
                    ? rich(
                        firstAlert.kind === 'failed' ? 'imports.failed' : 'imports.stuck',
                        firstAlert.at,
                        {
                          country: firstAlert.country,
                        },
                      )
                    : t('imports.scope')
                }
              />
            </li>
          )}
          {canCities && (
            <li>
              <AttentionTile
                {...link(paths.cities, { status: ['waitlist'] })}
                {...tile(waitlist)}
                title={t('waitlist.title')}
                value={cities.value}
                more={cities.more}
                tone="neutral"
              />
            </li>
          )}
        </AttentionTiles>
        <section className={styles.todo} aria-labelledby={todoId}>
          <h2 id={todoId} className={styles.heading}>
            {t('todo_title')}
          </h2>
          {loading && todo.length === 0 && !todoError ? (
            <Skeleton.Text lines={3} />
          ) : (
            <TodoList items={todo} label={t('todo_title')} error={todoError} />
          )}
        </section>
      </div>
    </>
  );
}
