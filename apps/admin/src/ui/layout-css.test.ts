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
