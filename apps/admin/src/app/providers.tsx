import { type QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { type ReactNode, useEffect, useRef } from 'react';
import type { Api } from '../api/client';
import { ApiProvider } from '../api/context';
import { DocumentTitle } from '../i18n/DocumentTitle';
import { LocaleProvider } from '../i18n/locale';
import { ShortcutsProvider } from '../ui/shortcuts';
import { ThemeProvider } from '../ui/theme';
import { ToastProvider, useToast } from '../ui/toast';
import { LeaveGuardProvider } from './LeaveGuardProvider';
import { SessionProvider, useSession } from './session';

// Провайдеры приложения в порядке зависимостей: тема (ни от чего не зависит; атрибут уже поставил
// public/theme-init.js) → клиент API → кэш запросов → сессия (me) → язык
// (me.locale) → тосты (подписи — на языке) → защита от потери (вопрос — на языке, useLogout видит формы) →
// горячие клавиши (шпаргалка — на языке; выше RouterProvider, а регистрации g x — в разделах под роутером). Те же
// провайдеры — в тестах (src/test/render.tsx).
export function AppProviders(props: { api: Api; queryClient: QueryClient; children: ReactNode }) {
  return (
    <ThemeProvider>
      <ApiProvider api={props.api}>
        <QueryClientProvider client={props.queryClient}>
          <SessionProvider>
            <LocaleProvider>
              <ToastProvider>
                <UndoReset />
                <DocumentTitle />
                <LeaveGuardProvider>
                  <ShortcutsProvider>{props.children}</ShortcutsProvider>
                </LeaveGuardProvider>
              </ToastProvider>
            </LocaleProvider>
          </SessionProvider>
        </QueryClientProvider>
      </ApiProvider>
    </ThemeProvider>
  );
}

// Тосты с «Отменить» снимаются при выходе и смене сотрудника (спека UI-фундамента §4.10): отмена от имени
// другого сотрудника недопустима. Сессия выше тостов (язык тостов — от me), поэтому подписка — здесь, под ними.
function UndoReset() {
  const s = useSession();
  const toast = useToast();
  const staffId = s.status === 'active' || s.status === 'setup' ? s.me.id : null;
  const owner = useRef(staffId);
  useEffect(() => {
    const prev = owner.current;
    owner.current = staffId;
    // сотрудник появился (загрузка me, вход) — снимать нечего и нечье; был другой или вышел — снять
    if (prev !== null && prev !== staffId) toast.dismissUndo();
  }, [staffId, toast]);
  return null;
}
