import type { IconName } from '@wf/icons';
import { type KeyboardEvent, type ReactNode, useEffect, useId, useMemo, useState } from 'react';
import { useTranslations } from 'use-intl';
import { isApiError } from '../../api/client';
import { type Command, COMMANDS, type CommandContext, type NavigateTo } from '../../app/commands';
import { isDesktopOnly } from '../../app/desktopOnly';
import { navFor } from '../../app/nav';
import { ENTITY_MIN_CHARS, type EntityKind, useEntitySearch } from '../../app/paletteSearch';
import { paths } from '../../app/paths';
import { can, decide, type Me } from '../../app/permissions';
import { pushRecent, readRecent, type RecentItem, type RecentKind } from '../../app/recent';
import { useSession } from '../../app/session';
import { messagesFor } from '../../i18n/messages';
import { cx } from '../cx';
import { useErrorText } from '../errorText';
import { Icon } from '../icon';
import { useIsHandheld, useIsMobile } from '../media';
import { Modal, Sheet } from '../overlay';
import { SEARCH_DEBOUNCE_MS } from '../table/model';
import { fuzzyRank } from './fuzzy';
import styles from './Palette.module.css';

// Командная палитра (спека UI-фундамента §4.1, план UF4.5). Modal (на узком — Sheet во всю высоту): стек оверлеев,
// ловушка фокуса, возврат фокуса. Открытие в адрес не пишется — мгновенный инструмент, а не состояние экрана.
// - Группы: «Недавнее», «Разделы» (navFor(me) + «Главная»), «Действия» (право действия; dual — «на подтверждение»;
//   на карманном устройстве без «только с компьютера»), сущности (app/paletteSearch.ts). Пустой запрос — недавнее и
//   разделы. Локальное ищет fuzzyRank (подпись, английская подпись, ключевые слова); подсветка — <mark>.
// - Недавнее — readRecent(me.id) на момент открытия; пункт, к которому пропал доступ (раздела нет в меню, нет права
//   сущности, чужой город модератора), не показывается; подпись раздела — по языку интерфейса.
// - Доступность — WAI-ARIA combobox + listbox: фокус остаётся в поле, активный пункт — aria-activedescendant; ↑/↓ по
//   кругу, Home/End, Enter — выполнить; Esc — поле не пусто: очистить, иначе закрыть (Overlay). Число результатов —
//   вежливая живая область после паузы ввода (SEARCH_DEBOUNCE_MS).
// Решает всегда сервер: фильтры палитры — удобство (как меню), не защита.

export type CommandPaletteProps = {
  open: boolean;
  onClose: () => void;
  context: CommandContext;
};

export function CommandPalette({ open, onClose, context }: CommandPaletteProps) {
  const t = useTranslations('palette');
  const mobile = useIsMobile();
  const Frame = mobile ? Sheet : Modal;
  return (
    <Frame open={open} onClose={onClose} title={t('title')}>
      <PaletteBody onClose={onClose} context={context} full={mobile} />
    </Frame>
  );
}

type Entry = {
  key: string;
  label: string;
  ranges: [number, number][];
  icon: IconName;
  detail?: string;
  shortcut?: string;
  approval?: boolean;
  select: () => void;
};

type Block = { id: string; title: string; entries: Entry[]; status?: ReactNode };

const RECENT_ICON: Record<RecentKind, IconName> = {
  section: 'list',
  city: 'city',
  staff: 'user',
  country: 'globe',
};
const ENTITY_ICON: Record<EntityKind, IconName> = { city: 'city', staff: 'user', country: 'globe' };
const ENTITY_GROUP: Record<EntityKind, 'cities' | 'staff' | 'countries'> = {
  city: 'cities',
  staff: 'staff',
  country: 'countries',
};

// Английская подпись команды — второе поле поиска: «city» находит «Города» при русском интерфейсе (подписи без
// параметров ICU — значение берётся как есть).
const EN = messagesFor('en');
function englishLabel(key: string): string {
  let node: unknown = EN;
  for (const part of key.split('.')) {
    node =
      typeof node === 'object' && node !== null
        ? (node as Record<string, unknown>)[part]
        : undefined;
  }
  return typeof node === 'string' ? node : '';
}

// Сохранённый адрес недавнего → навигация роутера (путь и search-параметры строками).
function parsePath(path: string): NavigateTo {
  const url = new URL(path, 'http://palette.local');
  const search = Object.fromEntries(url.searchParams);
  return Object.keys(search).length > 0 ? { to: url.pathname, search } : { to: url.pathname };
}

// Доступен ли пункт недавнего сейчас (права могли снять).
function recentAllowed(me: Me, item: RecentItem, sections: readonly Command[]): boolean {
  switch (item.kind) {
    case 'section':
      return sections.some((c) => c.id === item.id);
    case 'city':
      return can(me, 'geo.cities.read', item.id);
    case 'staff':
      return can(me, 'staff.accounts.read');
    case 'country':
      return can(me, 'geo.countries.read');
  }
}

function PaletteBody({
  onClose,
  context,
  full,
}: {
  onClose: () => void;
  context: CommandContext;
  full: boolean;
}) {
  const session = useSession();
  const me = session.status === 'active' ? session.me : null;
  if (!me) return null;
  return <Palette me={me} onClose={onClose} context={context} full={full} />;
}

function Palette({
  me,
  onClose,
  context,
  full,
}: {
  me: Me;
  onClose: () => void;
  context: CommandContext;
  full: boolean;
}) {
  const t = useTranslations('palette');
  const tr = useTranslations();
  const errorText = useErrorText();
  const handheld = useIsHandheld();
  const baseId = useId();
  const listId = `${baseId}-list`;
  const [query, setQuery] = useState('');
  const [term, setTerm] = useState('');
  const [activeKey, setActiveKey] = useState<string | null>(null);
  const staffId = me.id ?? '';

  // Пауза ввода: term — запрос сущностей и момент объявить число результатов.
  useEffect(() => {
    const next = query.trim();
    if (next === term) return;
    const timer = setTimeout(() => setTerm(next), SEARCH_DEBOUNCE_MS);
    return () => clearTimeout(timer);
  }, [query, term]);

  const sections = useMemo(() => {
    const menu = new Set(navFor(me).flatMap((g) => g.items.map((i) => i.path)));
    return COMMANDS.filter(
      (c) =>
        c.group === 'section' &&
        c.path !== undefined &&
        (c.path === paths.root || menu.has(c.path)) &&
        (c.permission == null || can(me, c.permission)),
    );
  }, [me]);
  const actions = useMemo(
    () =>
      COMMANDS.filter(
        (c) =>
          c.group === 'action' &&
          (c.permission == null || can(me, c.permission)) &&
          !(handheld && (c.desktopOnly || (c.permission != null && isDesktopOnly(c.permission)))),
      ),
    [me, handheld],
  );
  const [stored] = useState(() => readRecent(staffId));
  const recent = stored.filter((r) => recentAllowed(me, r, sections));

  const close = (then: () => void) => {
    onClose();
    then();
  };
  const runCommand = (c: Command) =>
    close(() => {
      if (c.group === 'section' && c.path) {
        pushRecent(staffId, { kind: 'section', id: c.id, label: tr(c.labelKey), path: c.path });
      }
      c.run(context);
    });

  const q = query.trim();
  const fieldsOf = (c: Command) => [
    tr(c.labelKey),
    englishLabel(c.labelKey),
    ...(c.keywordsKey ? [tr(c.keywordsKey)] : []),
  ];
  const commandEntries = (list: readonly Command[]): Entry[] =>
    fuzzyRank(q, list, { fields: fieldsOf }).map(({ item: c, ranges, field }) => ({
      key: c.id,
      label: tr(c.labelKey),
      ranges: field === 0 ? ranges : [],
      icon: c.icon ?? 'list',
      shortcut: c.shortcut,
      approval: c.permission != null && decide(me, c.permission) === 'needs_approval',
      select: () => runCommand(c),
    }));

  const recentLabel = (r: RecentItem) => {
    const c = r.kind === 'section' ? sections.find((s) => s.id === r.id) : undefined;
    return c ? tr(c.labelKey) : r.label;
  };
  const recentEntries: Entry[] = fuzzyRank(q, recent, { fields: (r) => [recentLabel(r)] }).map(
    ({ item: r, ranges }) => ({
      key: `recent:${r.kind}:${r.id}`,
      label: recentLabel(r),
      ranges,
      icon: RECENT_ICON[r.kind],
      select: () =>
        close(() => {
          const c = r.kind === 'section' ? sections.find((s) => s.id === r.id) : undefined;
          pushRecent(staffId, { ...r, label: recentLabel(r) });
          if (c) c.run(context);
          else context.navigate(parsePath(r.path));
        }),
    }),
  );

  const groups = useEntitySearch(term);
  const blocks: Block[] = [
    { id: 'recent', title: t('groups.recent'), entries: recentEntries },
    { id: 'sections', title: t('groups.sections'), entries: commandEntries(sections) },
  ];
  if (q !== '')
    blocks.push({ id: 'actions', title: t('groups.actions'), entries: commandEntries(actions) });
  const searching = q.length >= ENTITY_MIN_CHARS;
  if (searching) {
    for (const g of groups) {
      const status =
        g.status === 'loading'
          ? t('searching')
          : g.status === 'error'
            ? t('error', { text: errorText(isApiError(g.error) ? g.error : 'internal') })
            : undefined;
      blocks.push({
        id: g.kind,
        title: t(`groups.${ENTITY_GROUP[g.kind]}`),
        status,
        entries: g.items.map((hit) => ({
          key: `${hit.kind}:${hit.id}`,
          label: hit.label,
          ranges: [],
          icon: ENTITY_ICON[hit.kind],
          detail: hit.detail,
          select: () =>
            close(() => {
              pushRecent(staffId, { kind: hit.kind, id: hit.id, label: hit.label, path: hit.path });
              context.navigate(hit.to);
            }),
        })),
      });
    }
  }
  const visible = blocks.filter((b) => b.entries.length > 0 || b.status !== undefined);
  const options = visible.flatMap((b) => b.entries);
  const index = Math.max(
    0,
    options.findIndex((o) => o.key === activeKey),
  );
  const active = options[index];
  const optionId = (i: number) => `${listId}-${i}`;
  const pending = searching && groups.some((g) => g.status === 'loading');

  // Активный пункт — в видимой части списка (в jsdom scrollIntoView нет).
  useEffect(() => {
    document.getElementById(optionId(index))?.scrollIntoView?.({ block: 'nearest' });
  });

  const move = (to: number) => {
    const next = options[(to + options.length) % options.length];
    if (next) setActiveKey(next.key);
  };
  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Escape') {
      // поле не пусто — сперва очистить; Overlay видит defaultPrevented и не закрывает
      if (query !== '') {
        e.preventDefault();
        setQuery('');
        setActiveKey(null);
      }
      return;
    }
    if (e.nativeEvent.isComposing || options.length === 0) return;
    const keys: Record<string, () => void> = {
      ArrowDown: () => move(index + 1),
      ArrowUp: () => move(index - 1),
      Home: () => move(0),
      End: () => move(options.length - 1),
      Enter: () => active?.select(),
    };
    const action = keys[e.key];
    if (!action) return;
    e.preventDefault();
    action();
  };

  const settled = q !== '' && q === term && !pending;
  let position = 0;
  return (
    <div className={cx(styles.root, full && styles.full)}>
      <input
        className={styles.input}
        type="text"
        role="combobox"
        aria-label={t('input')}
        placeholder={t('input')}
        aria-expanded={options.length > 0}
        aria-controls={listId}
        aria-autocomplete="list"
        aria-activedescendant={active ? optionId(index) : undefined}
        autoComplete="off"
        spellCheck={false}
        value={query}
        onChange={(e) => {
          setQuery(e.target.value);
          setActiveKey(null);
        }}
        onKeyDown={onKeyDown}
      />
      <p className={styles.srOnly} aria-live="polite" data-palette-count="">
        {settled ? t('count', { count: options.length }) : ''}
      </p>
      <div role="listbox" id={listId} aria-label={t('title')} className={styles.list}>
        {visible.map((b) => {
          const labelId = `${listId}-${b.id}`;
          return (
            <div key={b.id} role="group" aria-labelledby={labelId} className={styles.group}>
              <div role="presentation" id={labelId} className={styles.groupLabel}>
                {b.title}
              </div>
              {b.entries.map((e) => {
                const i = position++;
                return (
                  <div
                    key={e.key}
                    id={optionId(i)}
                    role="option"
                    aria-selected={i === index}
                    data-active={i === index ? '' : undefined}
                    className={styles.option}
                    onClick={e.select}
                    onMouseMove={() => {
                      if (i !== index) setActiveKey(e.key);
                    }}
                  >
                    <Icon name={e.icon} size={16} />
                    <span className={styles.label} data-label="">
                      <Marked text={e.label} ranges={e.ranges} />
                    </span>
                    {e.detail && <span className={styles.detail}>{e.detail}</span>}
                    {e.approval && <span className={styles.approval}>{t('needs_approval')}</span>}
                    {e.shortcut && (
                      <kbd className={styles.kbd} aria-hidden="true">
                        {e.shortcut}
                      </kbd>
                    )}
                  </div>
                );
              })}
              {b.status && (
                <div role="presentation" className={styles.status}>
                  {b.status}
                </div>
              )}
            </div>
          );
        })}
      </div>
      {q !== '' && options.length === 0 && !pending && <p className={styles.empty}>{t('empty')}</p>}
    </div>
  );
}

// Подпись с подсветкой совпадения: <mark> (не только цвет — ещё и подчёркивание).
function Marked({ text, ranges }: { text: string; ranges: [number, number][] }) {
  if (ranges.length === 0) return text;
  const out: ReactNode[] = [];
  let at = 0;
  for (const [start, end] of ranges) {
    if (start > at) out.push(text.slice(at, start));
    out.push(
      <mark key={start} className={styles.mark}>
        {text.slice(start, end)}
      </mark>,
    );
    at = end;
  }
  if (at < text.length) out.push(text.slice(at));
  return out;
}
