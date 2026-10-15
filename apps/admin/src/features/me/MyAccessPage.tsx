import { useTranslations } from 'use-intl';
import { PageHeader, StatusChip } from '../../ui';
import { ChangePasswordCard } from './ChangePasswordCard';
import styles from './me.module.css';
import { MySessionsCard } from './MySessionsCard';
import { useMyMfa } from './queries';
import { SecondFactorCard } from './SecondFactorCard';

// «Мой доступ» (спека §9.4 п. 4, макет 07; право self — любому активному сотруднику; только с
// компьютера — desktopOnly в NAV): второй фактор и резервные коды, активные сеансы, смена пароля (*).
export function MyAccessPage() {
  const t = useTranslations();
  const mfa = useMyMfa();
  return (
    <>
      <PageHeader
        title={t('nav.me')}
        subtitle={t('me.subtitle')}
        actions={mfa.data?.confirmed_at && <StatusChip tone="success">{t('me.mfa_on')}</StatusChip>}
      />
      <div className={styles.grid}>
        <div className={styles.column}>
          <SecondFactorCard />
          <ChangePasswordCard />
        </div>
        <MySessionsCard />
      </div>
    </>
  );
}
