import { Outlet } from '@tanstack/react-router';
import { useEffect } from 'react';
import { useIncomingApprovalsCount } from '../features/approvals/queries';
import { LocaleSwitcher } from '../features/me/LocaleSwitcher';
import { AppShell } from '../ui';
import { forgetTablePositions } from '../ui/table/restore';
import { useAccountCard } from './account';
import { useLogout } from './logout';
import { NAV } from './nav';
import type { Me } from './permissions';
import { paths } from './paths';
import { useSession } from './session';

// Каркас активной стадии: AppShell с меню из NAV, карточкой сотрудника, языком и выходом. key по
// me.id: сменился сотрудник без выхода (другая вкладка, ревью ветки I1) — экраны прежнего
// размонтируются вместе с состоянием (открытые окна, коды, введённое), кэш стирает SessionProvider.
export function Shell() {
  const s = useSession();
  if (s.status !== 'active') return null;
  return <ActiveShell key={s.me.id} me={s.me} />;
}

function ActiveShell({ me }: { me: Me }) {
  const account = useAccountCard(me);
  // Выход — вход без return (logout.ts)
  const logout = useLogout();
  // число у «Подтверждений» — фоновый опрос (features/approvals/queries.ts)
  const incoming = useIncomingApprovalsCount();
  // строки, с которых уходили в карточки (возврат «Назад» — фокус), — только этого сотрудника
  useEffect(() => forgetTablePositions, []);
  return (
    <AppShell
      groups={NAV}
      account={account}
      counts={{ [paths.approvals]: incoming }}
      onLogout={() => logout.mutate()}
      loggingOut={logout.isPending}
      profileMenu={<LocaleSwitcher />}
    >
      <Outlet />
    </AppShell>
  );
}
