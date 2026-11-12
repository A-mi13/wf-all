/// <reference types="node" />
// Типы Node — только ради этого стража (читает стили с диска как текст): vitest отдаёт CSS-модуль
// прокси, а jsdom не считает раскладку — поэтому визуальные правила V1 проверяются по исходнику CSS.
import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { tokens } from '@wf/tokens';
import { expect, test } from 'vitest';

const dir = join(import.meta.dirname, '..');
const styles = (readdirSync(dir, { recursive: true }) as string[])
  .map((f) => f.replaceAll('\\', '/'))
  .filter((f) => f.endsWith('.css'));
const read = (f: string) => readFileSync(join(dir, f), 'utf8');

// Правила файла: селектор → объявления (комментарии сняты; вложенные @media — плоско, этого хватает).
function rules(f: string): { selector: string; body: string }[] {
  const css = read(f).replace(/\/\*[\s\S]*?\*\//g, '');
  return [...css.matchAll(/([^{}]+)\{([^{}]*)\}/g)].map((m) => ({
    selector: (m[1] ?? '').trim(),
    body: m[2] ?? '',
  }));
}

function rule(f: string, selector: string): string {
  const found = rules(f).filter((r) => r.selector === selector);
  expect(found, `${f}: ${selector}`).not.toHaveLength(0);
  return found.map((r) => r.body).join('\n');
}

test('стражу есть что читать', () => {
  expect(styles).toContain('ui/shell/Tabs.module.css');
  expect(styles).toContain('ui/table/DataTable.module.css');
  expect(styles).toContain('ui/Button.module.css');
});

// V1 п. 4: overflow-x: auto без overflow-y превращает overflow-y: visible в auto (CSS Overflow §3) —
// лишний пиксель по высоте (отрицательный margin, подчёркивание вкладки) рисует вертикальный
// скроллбар со стрелками. Горизонтальная прокрутка — только с явным overflow-y.
test('горизонтальная прокрутка объявляет overflow-y явно (без случайной вертикальной полосы)', () => {
  const bad = styles.flatMap((f) =>
    rules(f)
      .filter((r) => /overflow-x:\s*(auto|scroll)/.test(r.body) && !/overflow-y:/.test(r.body))
      .map((r) => `${f}: ${r.selector}`),
  );
  expect(bad).toEqual([]);
});

test('вкладки: список не прокручивается по вертикали, вкладка не вылезает за него отрицательным отступом', () => {
  expect(rule('ui/shell/Tabs.module.css', '.list')).toMatch(/overflow-y:\s*hidden/);
  expect(rule('ui/shell/Tabs.module.css', '.tab')).not.toMatch(/margin[\w-]*:\s*-/);
});

// V1 п. 5: кнопка-ссылка в ячейке («Кто» в журнале) при переносе центрировалась — у .button
// text-align: center и justify-content: center. Текстовая кнопка выравнивается как текст — по началу.
test('кнопка-ссылка выравнивает текст по началу строки', () => {
  const link = rule('ui/Button.module.css', '.link');
  expect(link).toMatch(/text-align:\s*start/);
  expect(link).toMatch(/justify-content:\s*flex-start/);
});

test('«Кто» в журнале: длинное имя переносится внутри столбца, не раздвигая его', () => {
  expect(rule('features/audit/audit.module.css', '.actor')).toMatch(/overflow-wrap:\s*anywhere/);
});

// V1 п. 6: кнопка «Ещё фильтры» — у правого края строки инструментов любой таблицы (журнал,
// сотрудники, города), «Показано N» — слева строкой ниже.
test('кнопка фильтров таблицы прижата к правому краю строки инструментов', () => {
  expect(rule('ui/table/DataTable.module.css', '.moreButton')).toMatch(
    /margin-inline-start:\s*auto/,
  );
});

// Переключатель языка «прыгал» при смене языка (пользователь, 28.10): ширина сегментов не должна
// зависеть от текста и состояния, а соседи по строке («Выйти» / «Sign out») — двигать его.
test('сегменты: равные колонки фиксированной ширины, выбранный — без смены метрик шрифта', () => {
  const root = rule('ui/Segmented.module.css', '.root');
  expect(root).toMatch(/display:\s*inline-grid/);
  // все сегменты — одной фиксированной ширины (не по тексту)
  expect(root).toMatch(/grid-auto-columns:\s*(\d+px|var\(--wf-[\w-]+\))/);
  expect(rule('ui/Segmented.module.css', '.segment')).toMatch(/width:\s*100%/);
  expect(rule('ui/Segmented.module.css', ".segment[aria-pressed='true']")).not.toMatch(
    /font|letter-spacing|padding|border-width/,
  );
});

// Экраны входа (поправка 10.11): справа от бренда — язык и тема. На 390 px в EN бренд + язык + тема (~348–368
// px) не помещаются в колонку (326 px) — строка переносится: переключатели целиком уходят своей строкой (сами не
// рвутся), без @media — по содержимому.
test('экраны входа: бренд и переключатели переносятся, переключатели — одним блоком', () => {
  const top = rule('features/auth/Auth.module.css', '.top');
  expect(top).toMatch(/display:\s*flex/);
  expect(top).toMatch(/flex-wrap:\s*wrap/);
  const switches = rule('features/auth/Auth.module.css', '.switches');
  expect(switches).toMatch(/display:\s*flex/);
  expect(switches).toMatch(/flex-wrap:\s*nowrap/);
  expect(switches).toMatch(/margin-left:\s*auto/);
});

test('карточка сотрудника: язык и тема — строками по содержимому, «Выйти» — своя строка во всю ширину', () => {
  const rows = rule('ui/shell/AppShell.module.css', '.accountActions');
  expect(rows).toMatch(/display:\s*grid/);
  // одна колонка во всю ширину карточки: строки не делят ширину и не переносятся
  expect(rows).toMatch(/grid-template-columns:\s*minmax\(0,\s*1fr\)\s*;/);
  expect(rows).toMatch(/justify-items:\s*start/);
  expect(rows).not.toMatch(/flex-wrap/);
  // «Выйти» растянута на всю строку, подпись (Sign out) не переносится
  const logout = rule('ui/shell/AppShell.module.css', '.accountActions .logout');
  expect(logout).toMatch(/justify-self:\s*stretch/);
  expect(logout).toMatch(/white-space:\s*nowrap/);
  expect(logout).not.toMatch(/grid-column:\s*2/);
});

test('вкладки: выбранная отличается цветом и подчёркиванием, не жирностью (соседи не сдвигаются)', () => {
  expect(rule('ui/shell/Tabs.module.css', ".tab[aria-selected='true']")).not.toMatch(
    /font|letter-spacing|padding/,
  );
});

// Ревью V1 m4 (спека UI-фундамента §6.1): в режиме высокой контрастности (forced-colors) браузер
// снимает box-shadow и цвета — базовая линия вкладок и выбранный сегмент пропадали. Системные цвета
// (CanvasText, Highlight) — не hex, страж csp.test.ts не задет.
test('forced-colors: у вкладок есть линия, у выбранного сегмента — рамка', () => {
  const forced = (f: string) => {
    const css = read(f).replace(/\/\*[\s\S]*?\*\//g, '');
    const at = css.indexOf('@media (forced-colors: active)');
    expect(at, `${f}: нет @media (forced-colors: active)`).toBeGreaterThanOrEqual(0);
    return css.slice(at);
  };
  expect(forced('ui/shell/Tabs.module.css')).toMatch(
    /\.list\s*\{[^}]*border-bottom:\s*1px solid CanvasText/,
  );
  expect(forced('ui/Segmented.module.css')).toMatch(
    /\.segment\[aria-pressed='true'\]\s*\{[^}]*border-color:\s*Highlight/,
  );
});

// V1 п. 9: в панели поиска места прокручивается только список, действие — в подвале окна.
test('список выбора таблицы прокручивается сам и заполняет высоту родителя; подвал окна не сжимается', () => {
  expect(rule('ui/table/DataTable.module.css', '.options')).toMatch(/overflow-y:\s*auto/);
  expect(rule('ui/table/DataTable.module.css', '.options')).toMatch(/min-height:\s*0/);
  expect(rule('ui/table/DataTable.module.css', '.fill')).toMatch(/min-height:\s*0/);
  expect(rule('features/geo/geo.module.css', '.fillPanel')).toMatch(/height:\s*100%/);
  expect(rule('features/geo/geo.module.css', '.searchStep')).toMatch(/min-height:\s*0/);
  expect(rule('ui/overlay/Overlay.module.css', '.footer')).toMatch(/flex-shrink:\s*0/);
});

// Пороги ширины (принцип 1, спека UI-фундамента §2.8, §3.4): @media — только по tokens.breakpoint,
// @container — по tokens.container или tokens.breakpoint; max-width — порог минус 0.02 (граница
// принадлежит следующему диапазону, как useIsMobile/useIsCompact). Случайное «500px» — мимо токенов.
const widthsIn = (prelude: string) =>
  [...prelude.matchAll(/\((min|max)-width:\s*([\d.]+)px\)/g)].map(
    (m) => [m[1] as 'min' | 'max', Number(m[2])] as const,
  );
function preludes(at: '@media' | '@container') {
  return styles.flatMap((f) =>
    [
      ...read(f)
        .replace(/\/\*[\s\S]*?\*\//g, '')
        .matchAll(new RegExp(`${at}([^{]*)\{`, 'g')),
    ].map((m) => ({ f, prelude: (m[1] ?? '').trim() })),
  );
}
const allowed = (kind: 'min' | 'max', px: number, thresholds: number[]) =>
  thresholds.some((t) => (kind === 'max' ? px === t - 0.02 : px === t));

test('ширины в @media — только из tokens.breakpoint', () => {
  const bp = Object.values(tokens.breakpoint);
  const bad = preludes('@media').flatMap(({ f, prelude }) => {
    const widths = widthsIn(prelude);
    // любая другая запись ширины (диапазон «width < 500px», rem, device-width) минует разбор — тоже ошибка
    // (ревью UF2a/I, m6)
    if ((prelude.match(/width/g) ?? []).length !== widths.length)
      return [`${f}: @media — ширина не в форме (min|max)-width: Npx — ${prelude}`];
    return widths
      .filter(([k, px]) => !allowed(k, px, bp))
      .map(([k, px]) => `${f}: @media ${k}-width ${px}px`);
  });
  expect(bad).toEqual([]);
});

test('ширины в @container — из tokens.container или tokens.breakpoint; запросы есть', () => {
  const thresholds = [...Object.values(tokens.container), ...Object.values(tokens.breakpoint)];
  const all = preludes('@container');
  expect(all.length).toBeGreaterThan(0);
  const bad = all.flatMap(({ f, prelude }) => {
    const widths = widthsIn(prelude);
    if (widths.length === 0 || (prelude.match(/width/g) ?? []).length !== widths.length)
      return [`${f}: @container — ширина не в форме (min|max)-width: Npx — ${prelude}`];
    return widths
      .filter(([k, px]) => !allowed(k, px, thresholds))
      .map(([k, px]) => `${f}: @container ${k}-width ${px}px`);
  });
  expect(bad).toEqual([]);
});

// Раскладка внутри страниц — по ширине своего контейнера (§3.4, принцип 6): область страницы каркаса
// и тело панели — контейнеры; Card-сетка, карточка и формы перестраиваются @container.
test('область страницы и тело панели — container-type: inline-size; сетка, карточка и формы — @container', () => {
  expect(rule('ui/shell/AppShell.module.css', '.main')).toMatch(/container-type:\s*inline-size/);
  expect(rule('ui/overlay/Overlay.module.css', '.body')).toMatch(/container-type:\s*inline-size/);
  const files = new Set(preludes('@container').map(({ f }) => f));
  for (const f of [
    'ui/table/DataTable.module.css',
    'ui/Card.module.css',
    'ui/form/Field.module.css',
  ])
    expect(files, f).toContain(f);
});

// Фокус не закрыт (WCAG 2.4.11, спека UI-фундамента §4.5, I13): у каждой области прокрутки, над которой что-то
// закреплено, — scroll-padding из токенов. Таблица — шапка и закреплённый столбец; тело панели — подвал; страница
// на телефоне — таббар (и тост с «Отменить», пока корень с data-toast-open).
test('таблица — своя область прокрутки по обеим осям, отступ прокрутки под шапку; закреплённый столбец — свой', () => {
  const scroll = rule('ui/table/DataTable.module.css', '.scroll');
  expect(scroll).toMatch(/overflow-x:\s*auto/);
  expect(scroll).toMatch(/overflow-y:\s*auto/);
  // шапка size.table-header (+ промежуток строк и кольцо фокуса)
  expect(scroll).toMatch(/scroll-padding-block-start:\s*calc\(var\(--wf-size-table-header\)/);
  expect(scroll).toMatch(/max-block-size:\s*calc\(100dvh/);
  for (const w of ['sm', 'md', 'lg'])
    expect(rule('ui/table/DataTable.module.css', `.pin-${w}`)).toMatch(
      /scroll-padding-inline-start:/,
    );
  expect(rule('ui/table/DataTable.module.css', '.stickyHeader .th')).toMatch(/position:\s*sticky/);
  expect(rule('ui/table/DataTable.module.css', '.pinned')).toMatch(/position:\s*sticky/);
});

// «Подробнее» в карточке телефона (ревью UF2b/I-Y, I1): атрибут hidden у ячейки не прячет её, если display: block
// карточки (.stacked .td) специфичнее — jsdom каскад не считает, поэтому страж по исходнику.
test('карточка телефона: скрытая ячейка «Подробнее» — display: none поверх display: block карточки', () => {
  expect(rule('ui/table/DataTable.module.css', '.stacked .td[hidden]')).toMatch(/display:\s*none/);
});

// UF2b.6 (n6, m23, R2-m3): высота подвала — токен size.overlay-footer, не замер (setCssPx в overlay/* запрещён CSP).
// Подвал — min-block-size по токену с border-box (кнопки в одну строку; при нехватке места ряд переносится вниз,
// а не выходит за край Drawer — фиксированной высоты нет); область тостов при открытой панели с подвалом встаёт
// над ним на тот же токен. Подвал — соседний flex-блок, тело он не перекрывает; перекрывает тело тост с
// «Отменить» (ревью UF2b/I-Y) — пока корень с data-toast-open, у тела отступ прокрутки и запас под тост.
test('подвал панели — min-block-size по size.overlay-footer, перенос кнопок, без фиксированной высоты', () => {
  const footer = rule('ui/overlay/Overlay.module.css', '.footer');
  expect(footer).toMatch(/min-block-size:\s*var\(--wf-size-overlay-footer\)/);
  expect(footer).toMatch(/box-sizing:\s*border-box/);
  expect(footer).toMatch(/flex-wrap:\s*wrap/);
  expect(footer).not.toMatch(/(^|[;\s])(height|block-size|max-height|max-block-size):/);
  expect(tokens.size['overlay-footer']).toBe(
    tokens.space['4'] + tokens.size.control + tokens.space['6'] + 1,
  );
});

test('тосты над подвалом открытой панели — отступ по size.overlay-footer; над шторкой — сверху', () => {
  expect(rule('ui/toast/Toast.module.css', ':global(:root[data-overlay-footer]) .region')).toMatch(
    /bottom:\s*calc\(var\(--wf-size-overlay-footer\)/,
  );
  const sheet = rule(
    'ui/toast/Toast.module.css',
    ":global(:root[data-overlay-footer='bottom']) .region",
  );
  expect(sheet).toMatch(/top:\s*var\(--wf-/);
  expect(sheet).toMatch(/bottom:\s*auto/);
});

// Запас под тост — только у панелей, которые тост перекрывает (ревью UF2b/II-A, m3): справа и шторка; окно по
// центру — только на телефоне (на широком тост ниже него). Общего правила для любого .body нет: центральное окно
// не меняет высоту на 94 px при появлении тоста.
const TOAST_ROOM = /padding-block-end:\s*calc\(var\(--wf-size-toast\)\s*\+\s*var\(--wf-space-6\)\)/;
test('тело панели при тосте с «Отменить» — отступ прокрутки и запас под тост у перекрываемых панелей', () => {
  for (const placement of ['.right', '.bottom']) {
    const body = rule(
      'ui/overlay/Overlay.module.css',
      `:global(:root[data-toast-open]) ${placement} .body`,
    );
    expect(body).toMatch(/scroll-padding-block-end:\s*calc\(var\(--wf-size-toast\)/);
    expect(body).toMatch(TOAST_ROOM);
  }
  expect(rules('ui/overlay/Overlay.module.css').map((r) => r.selector)).not.toContain(
    ':global(:root[data-toast-open]) .body',
  );
  const css = read('ui/overlay/Overlay.module.css').replace(/\/\*[\s\S]*?\*\//g, '');
  const at = css.indexOf('@media (max-width: 767.98px)');
  expect(at).toBeGreaterThanOrEqual(0);
  expect(css.slice(at)).toMatch(
    /:global\(:root\[data-toast-open\]\) \.center \.body\s*\{[^}]*padding-block-end:\s*calc\(var\(--wf-size-toast\)/,
  );
});

// Ревью UF2b/II-A, I1: на телефоне открыт оверлей без подвала (LeaveDialog, ConfirmDialog — действия в теле) — область
// тостов у нижнего края окна (space-2: таббар закрыт оверлеем), тост не выше size.toast, а запас тела (toast +
// space-6) больше space-2 + toast — нижние кнопки остаются над тостом.
test('телефон: оверлей без подвала — область тостов у края окна, ниже запаса тела', () => {
  const css = read('ui/toast/Toast.module.css').replace(/\/\*[\s\S]*?\*\//g, '');
  const at = css.indexOf('@media (max-width: 767.98px)');
  expect(at).toBeGreaterThanOrEqual(0);
  expect(css.slice(at)).toMatch(
    /:global\(:root\[data-overlay-open\]:not\(\[data-overlay-footer\]\)\) \.region\s*\{[^}]*bottom:\s*var\(--wf-space-2\)/,
  );
  expect(tokens.space['2'] + tokens.size.toast).toBeLessThan(tokens.size.toast + tokens.space['6']);
});

test('тост на телефоне — над таббаром, однострочный (многоточие), не выше size.toast', () => {
  const css = read('ui/toast/Toast.module.css').replace(/\/\*[\s\S]*?\*\//g, '');
  const at = css.indexOf('@media (max-width: 767.98px)');
  expect(at).toBeGreaterThanOrEqual(0);
  const mobile = css.slice(at);
  expect(mobile).toMatch(/\.region\s*\{[^}]*bottom:\s*var\(--wf-size-tabbar\)/);
  expect(mobile).toMatch(/\.toast\s*\{[^}]*max-block-size:\s*var\(--wf-size-toast\)/);
  expect(mobile).toMatch(/\.text\s*\{[^}]*white-space:\s*nowrap[^}]*\}/);
  expect(mobile).toMatch(/\.text\s*\{[^}]*text-overflow:\s*ellipsis/);
});

test('страница на телефоне — scroll-padding-block-end = таббар, с тостом — таббар + тост', () => {
  expect(rule('ui/shell/AppShell.module.css', ':global(:root)')).toMatch(
    /scroll-padding-block-end:\s*var\(--wf-size-tabbar\)/,
  );
  expect(rule('ui/shell/AppShell.module.css', ':global(:root[data-toast-open])')).toMatch(
    /scroll-padding-block-end:\s*calc\(var\(--wf-size-tabbar\)\s*\+\s*var\(--wf-size-toast\)\)/,
  );
  // scroll-padding не добавляет места прокрутки (ревью UF2b/I-Y, I2): с тостом нижний отступ страницы — не меньше
  // таббара и тоста, иначе последняя строка при прокрутке до конца остаётся под тостом
  expect(
    rule('ui/shell/AppShell.module.css', ':global(:root[data-toast-open]) .mobile .main'),
  ).toMatch(/padding-block-end:\s*calc\(var\(--wf-size-tabbar\)\s*\+\s*var\(--wf-size-toast\)/);
  // только на телефоне: правило внутри @media по breakpoint.mobile
  const css = read('ui/shell/AppShell.module.css').replace(/\/\*[\s\S]*?\*\//g, '');
  const at = css.indexOf(':global(:root)');
  expect(css.lastIndexOf('@media (max-width: 767.98px)', at)).toBeGreaterThanOrEqual(0);
});

// Ревью UF2a/I, m10: отступ body по умолчанию (8 px) + каркас высотой 100dvh давали прокрутку на 16 px на
// каждой странице. Сброс — в глобальном CSS, вместе с фоном документа.
test('body без отступа браузера', () => {
  expect(rule('ui/global.css', 'body')).toMatch(/margin:\s*0\s*;/);
});
