import { useNavigate, useSearch } from '@tanstack/react-router';
import { useTranslations } from 'use-intl';
import { paths } from '../../app/paths';
import { useSession } from '../../app/session';
import { PageHeader, Tabs } from '../../ui';
import { ApprovalsList } from './ApprovalsList';
import { useIncomingApprovalsCount } from './queries';
import { type ApprovalBox, parseApprovalsSearch } from './search';
import styles from './approvals.module.css';

// «Подтверждения» (*; спека §9.4 п. 6, §4.8, D17; группа «Безопасность», право self — любому
// активному; на телефоне открыт — D22): вкладки «Входящие» (ждут вашего решения, число — тот же
// фоновый счётчик, что в меню) и «Мои» (созданные вами, все состояния). Вкладка — search-параметр
// box. Решает всегда сервер: экран прячет заведомо лишнее и показывает его ответ.
export function ApprovalsPage() {
  const t = useTranslations('approvals');
  const tn = useTranslations('nav');
  const session = useSession();
  const navigate = useNavigate();
  const box: ApprovalBox = parseApprovalsSearch(useSearch({ strict: false })).box ?? 'incoming';
  const incoming = useIncomingApprovalsCount();
  if (session.status !== 'active') return null;

  return (
    <div className={styles.page}>
      <PageHeader title={tn('approvals')} subtitle={t('subtitle')} />
      <Tabs
        label={t('tabs.label')}
        items={[
          { value: 'incoming', label: t('tabs.incoming'), count: incoming },
          { value: 'mine', label: t('tabs.mine') },
        ]}
        value={box}
        onChange={(v) =>
          void navigate({
            to: paths.approvals,
            search: v === 'mine' ? { box: 'mine' } : {},
            replace: true,
          })
        }
      >
        <ApprovalsList key={box} box={box} me={session.me} />
      </Tabs>
    </div>
  );
}
