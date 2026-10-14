import type { ReactNode } from 'react';
import { useTranslations } from 'use-intl';
import { cx } from '../../ui/cx';
import styles from './Auth.module.css';

// Страница экранов входа вне каркаса (макеты 07 и 08 «Вход»): бренд, заголовок (единственный h1),
// пояснение, карточка с формой и подпись под ней. wide — настройка 2FA (QR рядом с секретом).
export function AuthLayout({
  title,
  lead,
  footnote,
  wide,
  children,
}: {
  title: ReactNode;
  lead?: ReactNode;
  footnote?: ReactNode;
  wide?: boolean;
  children: ReactNode;
}) {
  const t = useTranslations('app');
  return (
    <main className={styles.page}>
      <div className={cx(styles.column, wide && styles.wide)}>
        <p className={styles.brand}>{t('brand')}</p>
        <h1 className={styles.title}>{title}</h1>
        {lead && <p className={styles.lead}>{lead}</p>}
        <div className={styles.card}>{children}</div>
        {footnote && <p className={styles.footnote}>{footnote}</p>}
      </div>
    </main>
  );
}
