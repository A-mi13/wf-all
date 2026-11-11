import type { ReactNode } from 'react';
import { useTranslations } from 'use-intl';
import { isApiError } from '../../api/client';
import { Button } from '../Button';
import { cx } from '../cx';
import { useErrorText } from '../errorText';
import type { IconName } from '@wf/icons';
import { Icon, Illustration } from '../icon';
import { useOnline } from '../online';
import { Skeleton } from './Skeleton';
import { StateBlock } from './StateBlock';
import styles from './States.module.css';

// Состояния админки (спека §9.2, макет 07 «Состояния», 08 — баннер «нет сети»). Тексты — по
// умолчанию из states.*, экран может дать свои (например, «Очередь пуста» вместо «Ничего не
// нашлось» — хорошая новость, а не пустота).

// Пусто: по умолчанию — «Ничего не нашлось… Это не ошибка — просто пусто»; action — обычно
// «Сбросить фильтры». tone="accent" — пустая очередь как хорошая новость. illustration — рисунок набора
// (спека UI-фундамента §4.4: 48–64, декоративный — смысл в заголовке; экран → рисунок — design/README.md).
export function EmptyState({
  title,
  text,
  action,
  tone = 'neutral',
  illustration,
}: {
  title?: ReactNode;
  text?: ReactNode;
  action?: ReactNode;
  tone?: 'neutral' | 'accent';
  illustration?: IconName;
}) {
  const t = useTranslations('states.empty');
  return (
    <StateBlock
      dashed={tone === 'neutral'}
      tone={tone}
      illustration={illustration && <Illustration name={illustration} size={64} />}
      title={title ?? t('title')}
      text={text ?? t('text')}
      action={action}
    />
  );
}

// Ошибка загрузки: текст по коду (errors.<код>, откат — errors.internal), код для поддержки
// («Код ошибки: 500 · internal») и «Повторить». Ошибка не от API (нет сети) — без кода.
// retrying — перезапрос идёт: кнопка занята, повторный клик не уходит.
export function ErrorState({
  error,
  onRetry,
  retrying,
}: {
  error: unknown;
  onRetry: () => void;
  retrying?: boolean;
}) {
  const t = useTranslations('states.error');
  const ta = useTranslations('actions');
  const errorText = useErrorText();
  const api = isApiError(error) ? error : null;
  return (
    <StateBlock
      role="alert"
      tone="danger"
      title={t('title')}
      text={errorText(api ?? 'internal')}
      extra={
        api && <p className={styles.code}>{t('code', { status: api.status, code: api.code })}</p>
      }
      action={
        <Button variant="secondary" loading={retrying} onClick={onRetry}>
          {ta('retry')}
        </Button>
      }
    />
  );
}

// Скелетон строк вместо крутилки (обёртка над Skeleton.Table без шапки): высота списка не прыгает, когда
// данные приедут. Для читалки — «Загрузка…» (role="status"). Экраны его не рисуют — только DataTable;
// экран идёт через DataState (страж loading.test.ts).
export function LoadingRows({ rows = 5 }: { rows?: number }) {
  const t = useTranslations('states');
  return (
    <div role="status">
      <span className={styles.srOnly}>{t('loading')}</span>
      <Skeleton.Table columns={1} rows={rows} head={false} />
    </div>
  );
}

// Нет сети (спека §4.3): данных нет, запрос на паузе (networkMode 'online' — без сети TanStack Query ставит
// паузу и сам продолжит при возврате сети) или оборвался сетевым сбоем при «онлайн» браузере — тогда onRetry:
// событие online может не прийти (сеть «есть», сервера нет).
export function NoNetworkState({
  onRetry,
  retrying,
}: {
  onRetry?: () => void;
  retrying?: boolean;
}) {
  const t = useTranslations('states.no_network');
  const ta = useTranslations('actions');
  return (
    <StateBlock
      role="status"
      tone="warning"
      title={t('title')}
      text={t('text')}
      action={
        onRetry && (
          <Button variant="secondary" loading={retrying} onClick={onRetry}>
            {ta('retry')}
          </Button>
        )
      }
    />
  );
}

// Баннер «нет сети» (08): мутации без сети не копятся (networkMode 'always' — падают сразу),
// показано последнее загруженное. Живая область постоянна: появление текста объявляется.
export function OfflineBanner() {
  const t = useTranslations('states.offline');
  const online = useOnline();
  return (
    <div aria-live="polite" data-wf-offline="">
      {!online && (
        <div className={styles.banner}>
          <span className={styles.dot} aria-hidden="true" />
          <span>{t('text')}</span>
        </div>
      )}
    </div>
  );
}

// Прямой адрес раздела без права (D23): в меню такого раздела нет. scope 'block' — 403 внутри карточки или
// панели (DataState scope="block"): без h1 (у страницы он один) и «к этим данным», а не «к разделу».
export function ForbiddenState({ scope = 'page' }: { scope?: 'page' | 'block' }) {
  const t = useTranslations('states.forbidden');
  if (scope === 'block')
    return <StateBlock tone="muted" title={t('block_title')} text={t('block_text')} />;
  return <StateBlock page tone="muted" title={t('title')} text={t('text')} />;
}

// Пометка вместо действия на карманном устройстве (спека UI-фундамента §3.2; ставит ActionGate):
// выдача и снятие прав — только с компьютера. Текст, не кнопка и не disabled-кнопка: недоступное
// действие не попадает в Tab-порядок и не притворяется нажимаемым. compact — одна строка с иконкой
// (в панели, в шапке, в строке таблицы), иначе — блок с пояснением.
export function DesktopOnlyNotice({ compact }: { compact?: boolean }) {
  const t = useTranslations('states.desktop_only');
  if (compact)
    return (
      <p className={styles.desktopOnly} data-desktop-only="compact">
        <Icon name="monitor" size={16} />
        <span>{t('notice')}</span>
      </p>
    );
  return (
    <div className={cx(styles.block, styles.toneMuted)} data-desktop-only="block">
      <p className={styles.title}>{t('notice')}</p>
      <p className={styles.text}>{t('hint')}</p>
    </div>
  );
}

// Стадия error сессии: admin-api не ответил на GET /v1/staff/me (сеть, 5xx) — «нет связи». Ответил
// отказом (4xx, кроме 401 и 429 — у них свои экраны; ревью ветки M1: 403 auth.ip_not_allowed) — текст
// по коду и код для поддержки: связь есть, «проверьте подключение» ввело бы в заблуждение.
export function ConnectionErrorState({ error, onRetry }: { error?: unknown; onRetry: () => void }) {
  const t = useTranslations('states.connection');
  const te = useTranslations('states.error');
  const ta = useTranslations('actions');
  const errorText = useErrorText();
  if (isApiError(error) && error.status >= 400 && error.status < 500)
    return (
      <StateBlock
        page
        role="alert"
        tone="warning"
        title={t('refused_title')}
        text={errorText(error)}
        extra={
          <p className={styles.code}>{te('code', { status: error.status, code: error.code })}</p>
        }
        action={<Button onClick={onRetry}>{ta('retry')}</Button>}
      />
    );
  return (
    <StateBlock
      page
      role="alert"
      tone="warning"
      title={t('title')}
      text={t('text')}
      action={<Button onClick={onRetry}>{ta('retry')}</Button>}
    />
  );
}

// После 202: действие ушло на подтверждение второму сотруднику (правило двух рук).
export function PendingApprovalNotice({ action }: { action?: ReactNode }) {
  const t = useTranslations('states.pending_approval');
  return (
    <div role="status" className={styles.notice}>
      <p className={styles.title}>{t('title')}</p>
      <p className={styles.text}>{t('text')}</p>
      {action}
    </div>
  );
}
