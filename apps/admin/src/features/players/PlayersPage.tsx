import { useQueryClient } from '@tanstack/react-query';
import { useTranslations } from 'use-intl';
import type { components } from '../../api/gen/admin';
import { PageHeader, Skeleton } from '../../ui';
import { LookupForm } from './LookupForm';
import { PlayerCard } from './PlayerCard';
import { usePlayerLookup } from './queries';
import styles from './players.module.css';

type Player = components['schemas']['IdentityPlayerLookup'];

// «Игроки» (спека identity §7.4 «Поддержка: сброс 2FA»; план I-4, Task 9, R19, R20; группа меню «Игроки», право
// identity.pii.read — проверяет каркас; на телефоне открыт целиком, R27): поиск игрока по почте с причиной и
// карточка со сбросом 2FA. Почта, причина и результат — только в памяти экрана: ни адреса, ни storage, ни
// черновиков, ни «Недавнего»; уход со страницы стирает (кэш поиска — gcTime 0). Модератору сервер отдаёт только
// игроков его городов (чужой — 404, как несуществующий). Решает всегда сервер.
export function PlayersPage() {
  const t = useTranslations('players');
  const lookup = usePlayerLookup('players');
  const queryClient = useQueryClient();
  const { query, asked, busy } = lookup;
  const found = asked && !busy ? query.data : undefined;
  // отмена сброса (200): карточка — по ответу, повторного поиска нет (он потребовал бы снова причину, m7)
  const onCancelled = () =>
    queryClient.setQueryData<Player>(lookup.key, (p) => p && { ...p, mfa_reset: null });

  return (
    <div className={styles.page}>
      <PageHeader title={t('title')} subtitle={t('subtitle')} />
      <div className={styles.layout}>
        <section className={styles.panel} aria-label={t('lookup.title')}>
          <p className={styles.text}>{t('lookup.text')}</p>
          <LookupForm lookup={lookup} />
        </section>
        {busy && <Skeleton.Card />}
        {found && <PlayerCard key={found.id} player={found} onCancelled={onCancelled} />}
      </div>
    </div>
  );
}
