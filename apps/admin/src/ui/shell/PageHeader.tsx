import type { ReactNode } from 'react';
import { cx } from '../cx';
import styles from './PageHeader.module.css';

// Шапка страницы раздела (макет 07): заголовок — единственный h1 страницы, подзаголовок, справа —
// действия и чипы состояния («2ФА включена»). entity — страница сущности (город): заголовок — цель перехода
// «список → карточка» (view-transition-name: wf-entity, спека UI-фундамента §4.15).
export function PageHeader({
  title,
  subtitle,
  actions,
  entity = false,
}: {
  title: ReactNode;
  subtitle?: ReactNode;
  actions?: ReactNode;
  entity?: boolean;
}) {
  return (
    <header className={styles.header}>
      <div className={styles.text}>
        <h1 className={cx(styles.title, entity && styles.vtTarget)}>{title}</h1>
        {subtitle && <p className={styles.subtitle}>{subtitle}</p>}
      </div>
      {actions && <div className={styles.actions}>{actions}</div>}
    </header>
  );
}
