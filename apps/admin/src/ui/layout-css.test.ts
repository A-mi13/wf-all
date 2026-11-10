/// <reference types="node" />
// Типы Node — только ради этого стража (читает стили с диска как текст): vitest отдаёт CSS-модуль
// прокси, а jsdom не считает раскладку — поэтому визуальные правила V1 проверяются по исходнику CSS.
import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
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

test('карточка сотрудника: язык — своя колонка по содержимому, «Выйти» — в остатке строки и не переносит её', () => {
  const row = rule('ui/shell/AppShell.module.css', '.accountActions');
  expect(row).toMatch(/display:\s*grid/);
  expect(row).toMatch(/grid-template-columns:\s*max-content\s+minmax\(0,\s*1fr\)/);
  // «Выйти» — во второй колонке первой строки, справа, без переноса подписи
  const logout = rule('ui/shell/AppShell.module.css', '.accountActions .logout');
  expect(logout).toMatch(/grid-column:\s*2/);
  expect(logout).toMatch(/white-space:\s*nowrap/);
  expect(row).not.toMatch(/flex-wrap/);
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
