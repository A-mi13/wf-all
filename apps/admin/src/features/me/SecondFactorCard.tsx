import { type ReactNode, useId, useState } from 'react';
import { useTranslations } from 'use-intl';
import { useDateTime } from '../../i18n/format';
import { Button, ErrorState, LoadingRows, StatusChip } from '../../ui';
import styles from './me.module.css';
import { useMyMfa } from './queries';
import { RegenerateCodesModal } from './RegenerateCodesModal';

// Резервных кодов осталось мало — предупреждение (бриф Task 14: при ≤ 3).
const LOW_CODES = 3;
const TOTAL_CODES = 10;

// «Второй фактор» и «Резервные коды» (макет 07): статус (getStaffMfa), «Перевыпустить коды» (только
// TOTP, M9), «Потерял доступ» — текст: восстанавливают владелец или админ вдвоём. Сами коды после
// выдачи не показываются никогда — только «N из 10 не использованы».
export function SecondFactorCard() {
  const t = useTranslations('me');
  const titleId = useId();
  const lostId = useId();
  const dateTime = useDateTime();
  const mfa = useMyMfa();
  const [lost, setLost] = useState(false);
  const [regenerate, setRegenerate] = useState(false);

  let body: ReactNode;
  if (mfa.isPending) body = <LoadingRows rows={3} />;
  else if (mfa.isError)
    body = (
      <ErrorState error={mfa.error} onRetry={() => void mfa.refetch()} retrying={mfa.isFetching} />
    );
  else {
    const { confirmed_at: since, recovery_codes_left: left } = mfa.data;
    body = (
      <>
        <p className={styles.headline}>{since ? t('mfa.on') : t('mfa.off')}</p>
        {since && <p className={styles.text}>{t('mfa.since', { date: dateTime(since) })}</p>}
        <div className={styles.row}>
          {since && <Button onClick={() => setRegenerate(true)}>{t('mfa.regenerate')}</Button>}
          <Button
            variant="secondary"
            aria-expanded={lost}
            aria-controls={lostId}
            onClick={() => setLost((v) => !v)}
          >
            {t('mfa.lost')}
          </Button>
        </div>
        <div id={lostId} hidden={!lost} className={styles.lost}>
          <p className={styles.lostTitle}>{t('mfa.lost_title')}</p>
          <p className={styles.text}>{t('mfa.lost_text')}</p>
        </div>

        <div className={styles.spread}>
          <h3 className={styles.cardTitle}>{t('codes.title')}</h3>
          <StatusChip tone={left <= LOW_CODES ? 'warning' : 'neutral'}>
            {t('codes.left', { count: Math.min(left, TOTAL_CODES) })}
          </StatusChip>
        </div>
        {since && left <= LOW_CODES && <p className={styles.warning}>{t('codes.low')}</p>}
        <p className={styles.note}>{t('codes.text')}</p>
      </>
    );
  }

  return (
    <section aria-labelledby={titleId} className={styles.card}>
      <h2 id={titleId} className={styles.cardTitle}>
        {t('mfa.title')}
      </h2>
      {body}
      <RegenerateCodesModal open={regenerate} onClose={() => setRegenerate(false)} />
    </section>
  );
}
