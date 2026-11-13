import { type ReactNode, useState, useSyncExternalStore } from 'react';
import { LeaveDialog } from '../ui';
import { createLeaveStore, LeaveContext } from './leaveGuard';

// Защита от потери (leaveGuard.ts): реестр форм и вопрос «Уйти без сохранения?» — на приложение.
export function LeaveGuardProvider({ children }: { children: ReactNode }) {
  const [store] = useState(createLeaveStore);
  const question = useSyncExternalStore(store.subscribe, store.question);
  return (
    <LeaveContext value={store}>
      {children}
      <LeaveDialog
        open={question !== null}
        title={question?.title}
        text={question?.text}
        onStay={() => store.answer(false)}
        onLeave={() => store.answer(true)}
      />
    </LeaveContext>
  );
}
