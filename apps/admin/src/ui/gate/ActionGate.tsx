import type { ReactNode } from 'react';
import { isDesktopOnly } from '../../app/desktopOnly';
import { useIsHandheld } from '../media';
import { DesktopOnlyNotice } from '../states';

// «Только с компьютера» для действия (спека UI-фундамента §3.2): на карманном устройстве
// (useIsHandheld — палец и экран уже compact), если хоть одно право в реестре
// (src/app/desktopOnly.ts), вместо действия — пометка DesktopOnlyNotice; на компьютере при любой
// ширине и масштабе — само действие. permission — право САМОГО действия (m5). Ограничение
// интерфейса: решает сервер. Реестр — данные приложения, а не набора компонентов (U11, как AppShell
// → app/session).
export function ActionGate({
  permission,
  children,
  compact,
}: {
  permission: string | readonly string[];
  children: ReactNode;
  compact?: boolean;
}) {
  const handheld = useIsHandheld();
  if (handheld && isDesktopOnly(permission)) return <DesktopOnlyNotice compact={compact} />;
  return children;
}
