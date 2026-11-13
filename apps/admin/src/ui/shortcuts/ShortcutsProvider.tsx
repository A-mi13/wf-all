import {
  type ReactNode,
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  useSyncExternalStore,
} from 'react';
import { useTranslations } from 'use-intl';
import { useIsHandheld } from '../media';
import { hasModalOverlay } from '../overlay/stack';
import { eventKey, isEditableTarget, isModifierKey, SEQUENCE_MS } from './combo';
import { useSingleKeys } from './prefs';
import { ShortcutsHelp, type ShortcutsHelpItem } from './ShortcutsHelp';
import {
  createShortcutsStore,
  OpenHelpContext,
  ShortcutsContext,
  type ShortcutsStore,
  useShortcut,
} from './useShortcut';

// Горячие клавиши (спека UI-фундамента §4.2, план UF4.6): один слушатель keydown на document. Не срабатывают:
// идёт ввод IME (isComposing); фокус в поле (input, textarea, select, contenteditable, combobox, textbox), кроме
// регистраций allowInInput; зажаты Ctrl/Alt/Meta, кроме своих сочетаний (совпадение — по шагу целиком);
// открыта модальная запись стека (hasModalOverlay — открытая подсказка или список Select клавиши не глушат,
// R2-1; Esc модалки обрабатывает сам Overlay). Односимвольные выключаются переключателем шпаргалки (WCAG 2.1.4),
// Ctrl+K — всегда. На карманном устройстве регистрации с правом из реестра «только с компьютера» молчат и в
// шпаргалке их нет. Шпаргалка — «?», строится из реестра.
export function ShortcutsProvider({ children }: { children: ReactNode }) {
  const [store] = useState(createShortcutsStore);
  const [single, setSingle] = useSingleKeys();
  const handheld = useIsHandheld();
  const current = useRef({ single, handheld });
  useLayoutEffect(() => {
    current.current = { single, handheld };
  }, [single, handheld]);

  useEffect(() => {
    let pending: string[] = [];
    let timer: number | undefined;
    const reset = () => {
      pending = [];
      window.clearTimeout(timer);
      timer = undefined;
    };
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.defaultPrevented || e.isComposing || isModifierKey(e)) return;
      if (hasModalOverlay()) {
        reset();
        return;
      }
      const { single, handheld } = current.current;
      const inInput = isEditableTarget(e.target);
      const active = store
        .list()
        .filter(
          (r) =>
            (single || !r.combo.single) &&
            !(handheld && r.desktopOnly) &&
            (!inInput || r.allowInInput),
        );
      // seq — нажатые шаги: целиком совпала — действие; начало чьей-то последовательности — ждать следующий шаг
      const attempt = (seq: string[]): boolean => {
        const key = seq.join(' ');
        const hit = active.findLast((r) => r.combo.key === key);
        if (hit) {
          reset();
          e.preventDefault();
          hit.run();
          return true;
        }
        if (active.some((r) => r.combo.key.startsWith(key + ' '))) {
          reset();
          pending = seq;
          timer = window.setTimeout(reset, SEQUENCE_MS);
          return true;
        }
        return false;
      };
      const step = eventKey(e);
      if (pending.length > 0 && attempt([...pending, step])) return;
      reset();
      attempt([step]);
    };
    document.addEventListener('keydown', onKeyDown);
    return () => {
      document.removeEventListener('keydown', onKeyDown);
      reset();
    };
  }, [store]);

  const [helpOpen, setHelpOpen] = useState(false);
  const openHelp = useCallback(() => setHelpOpen(true), []);
  return (
    <ShortcutsContext value={store}>
      <OpenHelpContext value={openHelp}>
        {children}
        <Help
          store={store}
          open={helpOpen}
          setOpen={setHelpOpen}
          single={single}
          onSingleChange={setSingle}
          handheld={handheld}
        />
      </OpenHelpContext>
    </ShortcutsContext>
  );
}

function Help(props: {
  store: ShortcutsStore;
  open: boolean;
  setOpen: (open: boolean) => void;
  single: boolean;
  onSingleChange: (on: boolean) => void;
  handheld: boolean;
}) {
  const t = useTranslations('ui.shortcuts');
  const { open, setOpen } = props;
  useShortcut('?', () => setOpen(true), { label: t('help') });
  const list = useSyncExternalStore(props.store.subscribe, props.store.list, props.store.list);
  // Одна строка на клавишу — та, что сработает (последняя регистрация, как у слушателя): при конфликте (ошибка
  // разработки) в проде нет дублей строк и ключей React.
  const byKey = new Map<string, ShortcutsHelpItem>();
  for (const r of list) {
    if (props.handheld && r.desktopOnly) continue;
    byKey.delete(r.combo.key);
    if (r.label) byKey.set(r.combo.key, { id: r.combo.key, label: r.label, steps: r.combo.steps });
  }
  const items = [...byKey.values()];
  return (
    <ShortcutsHelp
      open={open}
      onClose={() => setOpen(false)}
      items={items}
      single={props.single}
      onSingleChange={props.onSingleChange}
    />
  );
}
