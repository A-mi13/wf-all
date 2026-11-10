import { useEffect } from 'react';
import { useTranslations } from 'use-intl';
import { ApiError } from '../../api/client';
import type { Story, StoryMeta } from '../../showcase/csf';
import styles from '../../showcase/Stories.module.css';
import { Button } from '../Button';
import { RateLimitedState } from './RateLimitedState';
import {
  ConnectionErrorState,
  EmptyState,
  ErrorState,
  ForbiddenState,
  LoadingRows,
  OfflineBanner,
  PendingApprovalNotice,
} from './states';

// Состояния данных (принцип 9): скелетон, пусто, ошибка с кодом и «Повторить», нет сети, нет прав; а также
// «нет связи с сервером», лимит запросов и «отправлено на подтверждение».
export default { title: 'UI/States', visual: true } satisfies StoryMeta;

const noop = () => undefined;

export const Loading: Story = () => <LoadingRows rows={4} />;

export const Empty: Story = () => {
  const t = useTranslations('geo.cities');
  return (
    <div className={styles.column}>
      <EmptyState />
      <EmptyState
        tone="accent"
        title={t('empty_title')}
        text={t('empty_text')}
        action={<Button>{t('open')}</Button>}
      />
    </div>
  );
};

export const Error: Story = () => (
  <div className={styles.column}>
    <ErrorState error={new ApiError({ status: 500, code: 'internal' })} onRetry={noop} />
    <ErrorState error={new TypeError('Failed to fetch')} onRetry={noop} retrying />
  </div>
);

// Нет сети: баннер показывает только офлайн-браузер — история выключает navigator.onLine на время показа.
// Своё свойство поверх геттера прототипа; уход с истории его снимает — снова настоящее значение.
export const Offline: Story = () => {
  useEffect(() => {
    Object.defineProperty(navigator, 'onLine', { configurable: true, get: () => false });
    window.dispatchEvent(new Event('offline'));
    return () => {
      delete (navigator as { onLine?: boolean }).onLine;
      window.dispatchEvent(new Event('online'));
    };
  }, []);
  return <OfflineBanner />;
};

export const Forbidden: Story = () => <ForbiddenState />;

export const Connection: Story = () => (
  <div className={styles.column}>
    <ConnectionErrorState onRetry={noop} />
    <ConnectionErrorState
      error={new ApiError({ status: 403, code: 'auth.ip_not_allowed' })}
      onRetry={noop}
    />
  </div>
);

export const RateLimited: Story = () => <RateLimitedState retryAfter={42} onRetry={noop} />;
// отсчёт идёт по настоящим секундам — снимок нестабилен
RateLimited.meta = { title: 'UI/States', visual: false };

export const PendingApproval: Story = () => <PendingApprovalNotice />;
