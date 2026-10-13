import type { ReactNode } from 'react';
import { useTranslations } from 'use-intl';
import { isApiError } from '../../api/client';
import { Button } from '../Button';
import { useErrorText } from '../errorText';
import { useOnline } from '../online';
import { StateBlock } from './StateBlock';
import styles from './States.module.css';

// Состояния админки (спека §9.2, макет 07 «Состояния», 08 — баннер «нет сети»). Тексты — по
// умолчанию из states.*, экран может дать свои (например, «Очередь пуста» вместо «Ничего не
// нашлось» — хорошая новость, а не пустота).

// Пусто: по умолчанию — «Ничего не нашлось… Это не ошибка — просто пусто»; action — обычно
// «Сбросить фильтры». tone="accent" — пустая очередь как хорошая новость.
export function EmptyState({
  title,
  text,
  action,
  tone = 'neutral',
}: {
  title?: ReactNode;
  text?: ReactNode;
  action?: ReactNode;
  tone?: 'neutral' | 'accent';
}) {
  const t = useTranslations('states.empty');
  return (
    <StateBlock
      dashed={tone === 'neutral'}
      tone={tone}
      title={title ?? t('title')}
      text={text ?? t('text')}
      action={action}
    />
  );
}

// Ошибка загрузки: текст по коду (errors.<код>, откат — errors.internal), код для поддержки
// («Код ошибки: 500 · internal») и «Повторить». Ошибка не от API (нет сети) — без кода.
export function ErrorState({ error, onRetry }: { error: unknown; onRetry: () => void }) {
  const t = useTranslations('states.error');
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
        <Button variant="secondary" onClick={onRetry}>
          {t('retry')}
        </Button>
      }
    />
  );
}

// Скелетон строк вместо крутилки: высота списка не прыгает, когда данные приедут. Для читалки —
// «Загрузка…» (role="status").
export function LoadingRows({ rows = 5 }: { rows?: number }) {
  const t = useTranslations('states');
  return (
    <div role="status" className={styles.skeleton}>
      <span className={styles.srOnly}>{t('loading')}</span>
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className={styles.skeletonRow} aria-hidden="true" data-skeleton-row="" />
      ))}
    </div>
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

// Прямой адрес раздела без права (D23): в меню такого раздела нет.
export function ForbiddenState() {
  const t = useTranslations('states.forbidden');
  return <StateBlock page tone="muted" title={t('title')} text={t('text')} />;
}

// Раздел на телефоне закрыт (D22, макет 08): выдача прав и настройки — только с компьютера.
export function DesktopOnlyState() {
  const t = useTranslations('states.desktop_only');
  return <StateBlock page tone="warning" title={t('title')} text={t('text')} />;
}

// Стадия error сессии: admin-api не ответил на GET /v1/staff/me (сеть, 5xx).
export function ConnectionErrorState({ onRetry }: { onRetry: () => void }) {
  const t = useTranslations('states.connection');
  return (
    <StateBlock
      page
      role="alert"
      tone="warning"
      title={t('title')}
      text={t('text')}
      action={<Button onClick={onRetry}>{t('retry')}</Button>}
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
