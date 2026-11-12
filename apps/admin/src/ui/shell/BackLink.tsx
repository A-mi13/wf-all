import { useNavigate, useRouterState } from '@tanstack/react-router';
import {
  createContext,
  type MouseEvent,
  type ReactNode,
  useContext,
  useEffect,
  useState,
} from 'react';
import { useTranslations } from 'use-intl';
import { isPanelKey } from '../../app/paths';
import styles from './BackLink.module.css';

// Последний адрес (с search-параметрами) каждого открытого пути — чтобы «Назад» вернул родителя с
// его фильтрами, страницей списка и вкладкой (состояние — в адресе). Ключи открытых панелей (PANEL_SEARCH_KEYS,
// спека UI-фундамента §4.6) вырезаются: возврат к родителю не открывает снова пустую панель. Живёт в каркасе: AppShell
// пересоздаётся на другого сотрудника (key={me.id}), и чужие фильтры следующему не достаются.
const BackMemory = createContext<ReadonlyMap<string, string>>(new Map());

// Пары search как записал роутер (кодировку не трогаем: URLSearchParams переписал бы пробел в «+»), без ключей панелей.
function withoutPanels(pathname: string, searchStr: string, hash: string): string {
  const pairs = searchStr
    .replace(/^\?/, '')
    .split('&')
    // ключи панелей — латиница: сравниваются без декодирования (негодный %-код в адресе не бросит)
    .filter((p) => p !== '' && !isPanelKey(p.split('=')[0] ?? ''));
  return `${pathname}${pairs.length > 0 ? `?${pairs.join('&')}` : ''}${hash ? `#${hash}` : ''}`;
}

export function BackMemoryProvider({ children }: { children: ReactNode }) {
  const location = useRouterState({ select: (s) => s.location });
  const [memory] = useState(() => new Map<string, string>());
  // Запись — после отрисовки адреса: страница-ребёнок, открытая следующей навигацией, читает адрес
  // родителя, записанный, пока родитель был на экране (в том числе после replace фильтров).
  useEffect(() => {
    memory.set(
      location.pathname,
      withoutPanels(location.pathname, location.searchStr, location.hash),
    );
  }, [memory, location.pathname, location.searchStr, location.hash]);
  return <BackMemory.Provider value={memory}>{children}</BackMemory.Provider>;
}

// Возврат к родителю вложенного экрана (страница города → «Города и запуск»): ссылка «← раздел»
// на последний адрес родителя в этой вкладке (фильтры и страница списка — из адреса); родитель ещё
// не открывался (прямая ссылка) — его адрес без параметров. Ссылка, а не кнопка: открывается и в
// новой вкладке; браузерная «Назад» работает своим путём (история не трогается).
export function BackLink({ to, label }: { to: string; label: string }) {
  const t = useTranslations('nav');
  const navigate = useNavigate();
  const memory = useContext(BackMemory);
  const href = memory.get(to) ?? to;
  const onClick = (e: MouseEvent<HTMLAnchorElement>) => {
    // Ctrl/⌘/Shift/средняя кнопка — новая вкладка или окно браузером
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) {
      return;
    }
    e.preventDefault();
    void navigate({ href });
  };
  return (
    <a href={href} className={styles.back} onClick={onClick}>
      <svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true" className={styles.icon}>
        <path d="M15 5 L8 12 L15 19" />
      </svg>
      <span className={styles.srOnly}>{t('back')}</span> {label}
    </a>
  );
}
