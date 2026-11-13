import type { ReactNode, Ref } from 'react';
import { useTranslations } from 'use-intl';
import { ThemeSwitch } from '../../ui';
import { cx } from '../../ui/cx';
import { DeviceLocaleSwitcher } from '../me/LocaleSwitcher';
import styles from './Auth.module.css';

// Страница экранов входа вне каркаса (макеты 07 и 08 «Вход»): бренд, заголовок (единственный h1),
// пояснение, карточка с формой и подпись под ней. wide — настройка 2FA (QR рядом с секретом);
// below — действие под карточкой («Выйти» на настройке 2FA). titleRef — заголовок принимает фокус
// программно (tabIndex -1): кнопка шага исчезла, фокус — сюда, а не на body. Над заголовком справа от
// бренда — язык и тема (спека UI-фундамента §4.12, поправка 10.11): свойства устройства, есть и до входа; язык —
// только пока нет учётки (на setup он из me.locale). Узкая колонка — переключатели своей строкой (перенос).
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
        <div className={styles.top}>
          <p className={styles.brand}>{t('brand')}</p>
          <div className={styles.switches}>
            <DeviceLocaleSwitcher />
            <ThemeSwitch />
          </div>
        </div>
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
