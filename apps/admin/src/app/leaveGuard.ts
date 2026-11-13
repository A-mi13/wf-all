import { useBlocker } from '@tanstack/react-router';
import { createContext, useCallback, useContext, useEffect, useLayoutEffect, useRef } from 'react';

// Защита от потери (спека UI-фундамента §4.11, N4, R2-2). useLeaveGuard(dirty) поверх useBlocker TanStack
// Router 1.170: переход внутри приложения (ссылка, меню, «Назад» браузера и жесты, палитра, g x) при dirty —
// свой вопрос «Уйти без сохранения?» (LeaveDialog, фокус на «Остаться»); F5 и закрытие вкладки — стандартный
// диалог браузера (enableBeforeUnload: dirty — чистая форма beforeunload не отменяет); «Выйти» — вопрос в
// useLogout до POST (useConfirmLeave): редирект на вход роутер ведёт с ignoreBlocker, блокировщик там не спросил бы.
// Один вопрос на один уход: блокирует только первая по реестру грязная форма (у каждого useBlocker свой вызов
// на переход — две грязные формы иначе спросили бы дважды); вопрос, уже открытый, — общий для всех спрашивающих.
// Исключений по ключу панели нет: закрытие панели своими средствами спрашивает Overlay.dirty и идёт навигацией
// с ignoreBlocker: true (UF2b.8).
//
// Опции сверены по исходникам @tanstack/react-router 1.170.46 (src/useBlocker.tsx): shouldBlockFn (возвращает
// true — блокировать; может быть промисом), withResolver (false — промис shouldBlockFn и есть ответ),
// enableBeforeUnload: boolean | () => boolean; ignoreBlocker — у navigate (router-core link.ts) и у
// history.back/go (@tanstack/history 1.162.5).
//
// Успех формы: сначала сбросить dirty, переход — после применения (следующим эффектом) или с ignoreBlocker:
// блокировщик читает dirty, применённый в последнем рендере, а синхронный переход в том же обработчике увидел
// бы прежнее значение.

type Entry = { dirty: boolean; title?: string; text?: string };
type Question = {
  title?: string;
  text?: string;
  answer: Promise<boolean>;
  resolve(leave: boolean): void;
};

export function createLeaveStore() {
  const entries: Entry[] = [];
  let question: Question | null = null;
  const listeners = new Set<() => void>();
  const notify = () => listeners.forEach((fn) => fn());
  const answer = (leave: boolean) => {
    const q = question;
    if (!q) return;
    question = null;
    q.resolve(leave);
    notify();
  };
  return {
    register(entry: Entry) {
      entries.push(entry);
      return () => {
        const i = entries.indexOf(entry);
        if (i !== -1) entries.splice(i, 1);
        // Вопрос пережил спрашивающих (форма снята мимо блокировщика: конец сессии, смена сотрудника — m1):
        // «Остаться» — отложенный переход отменяется, вопрос не висит над входом.
        if (question && !entries.some((e) => e.dirty)) answer(false);
      };
    },
    firstDirty: (): Entry | undefined => entries.find((e) => e.dirty),
    // Спросить «Уйти без сохранения?»: true — уйти. Открытый вопрос — общий.
    ask(opts: { title?: string; text?: string } = {}): Promise<boolean> {
      if (question) return question.answer;
      let resolve!: (leave: boolean) => void;
      const answer = new Promise<boolean>((r) => (resolve = r));
      question = { ...opts, answer, resolve };
      notify();
      return answer;
    },
    answer,
    question: () => question,
    subscribe(fn: () => void) {
      listeners.add(fn);
      return () => {
        listeners.delete(fn);
      };
    },
  };
}

export type LeaveStore = ReturnType<typeof createLeaveStore>;
// Реестр грязных форм и сам вопрос — на приложение (LeaveGuardProvider в AppProviders): useLogout видит формы
// любого экрана.
export const LeaveContext = createContext<LeaveStore | null>(null);

function useLeaveStore(): LeaveStore {
  const store = useContext(LeaveContext);
  if (!store) throw new Error('useLeaveGuard: нет LeaveGuardProvider выше по дереву');
  return store;
}

export function useLeaveGuard(dirty: boolean, opts: { title?: string; text?: string } = {}): void {
  const store = useLeaveStore();
  const entry = useRef<Entry>({ dirty: false });
  useEffect(() => store.register(entry.current), [store]);
  // До эффектов перехода (успех формы → переход эффектом): блокировщик читает уже сброшенное.
  useLayoutEffect(() => {
    entry.current.dirty = dirty;
    entry.current.title = opts.title;
    entry.current.text = opts.text;
  }, [dirty, opts.title, opts.text]);
  const shouldBlockFn = useCallback(async () => {
    const own = entry.current;
    if (!own.dirty || store.firstDirty() !== own) return false;
    const leave = await store.ask({ title: own.title, text: own.text });
    return !leave;
  }, [store]);
  useBlocker({ shouldBlockFn, withResolver: false, enableBeforeUnload: dirty });
}

// Есть несохранённое — спросить; true — можно уходить (useLogout до POST).
export function useConfirmLeave(): () => Promise<boolean> {
  const store = useLeaveStore();
  return useCallback(async () => {
    const entry = store.firstDirty();
    if (!entry) return true;
    return store.ask({ title: entry.title, text: entry.text });
  }, [store]);
}
