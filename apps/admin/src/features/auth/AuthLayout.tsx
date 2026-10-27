import type { ReactNode, Ref } from 'react';
import { useTranslations } from 'use-intl';
import { cx } from '../../ui/cx';
import styles from './Auth.module.css';

// Страница экранов входа вне каркаса (макеты 07 и 08 «Вход»): бренд, заголовок (единственный h1),
// пояснение, карточка с формой и подпись под ней. wide — настройка 2FA (QR рядом с секретом);
// below — действие под карточкой («Выйти» на настройке 2FA). titleRef — заголовок принимает фокус
// программно (tabIndex -1): кнопка шага исчезла, фокус — сюда, а не на body.
export function AuthLayout({
  title,
  lead,
  footnote,
  wide,
  below,
  titleRef,
  children,
}: {
  title: ReactNode;
  lead?: ReactNode;
  footnote?: ReactNode;
  wide?: boolean;
  below?: ReactNode;
  titleRef?: Ref<HTMLHeadingElement>;
  children: ReactNode;
}) {
  const t = useTranslations('app');
  return (
    <main className={styles.page}>
      <div className={cx(styles.column, wide && styles.wide)}>
        <p className={styles.brand}>{t('brand')}</p>
        <h1 ref={titleRef} tabIndex={titleRef ? -1 : undefined} className={styles.title}>
          {title}
        </h1>
        {lead && <p className={styles.lead}>{lead}</p>}
        <div className={styles.card}>{children}</div>
        {footnote && <p className={styles.footnote}>{footnote}</p>}
        {below && <div className={styles.below}>{below}</div>}
      </div>
    </main>
  );
}
