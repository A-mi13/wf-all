import { useSearch } from '@tanstack/react-router';
import { useTranslations } from 'use-intl';
import { usePanelParam } from '../../app/panels';
import { can } from '../../app/permissions';
import { useSession } from '../../app/session';
import { PageHeader } from '../../ui';
import { ApplicationDrawer } from './ApplicationDrawer';
import { CaptainsList } from './CaptainsList';
import { READ_PERMISSION } from './queries';
import { parseCaptainsSearch } from './search';
import styles from './captains.module.css';

// «Капитаны» (спека identity §7.4; план I-4, Task 8; группа меню «Игроки», право
// identity.captain_applications.read — проверяет каркас; на телефоне открыт целиком, решения — не staff.*, R27):
// очередь заявок с фильтрами в адресе и карточка заявки — панель ?application=<id> (R21: F5, «Назад» и
// пересланная ссылка её не теряют; без права ключ снимается). Решает всегда сервер.
export function CaptainsPage() {
  const t = useTranslations('captains');
  const session = useSession();
  const search = parseCaptainsSearch(useSearch({ strict: false }));
  const me = session.status === 'active' ? session.me : null;
  const panel = usePanelParam('application', {
    allowed: me !== null && can(me, READ_PERMISSION),
  });
  if (!me) return null;

  return (
    <div className={styles.page}>
      <PageHeader title={t('title')} subtitle={t('subtitle')} />
      <CaptainsList
        me={me}
        search={search}
        activeId={panel.value}
        onOpen={(a) => panel.open(a.id)}
      />
      <ApplicationDrawer me={me} id={panel.value} onClose={panel.close} />
    </div>
  );
}
