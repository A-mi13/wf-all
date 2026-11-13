// tokens.json (contracts/) → tokens.css + tokens.ts. Источник один на веб, админку и позже Flutter.
// Спека UI-фундамента §2.1–§2.2, §2.8: цвет — строка (одна на обе темы) или пара { dark, light } →
// light-dark(<светлое>, <тёмное>); тема — color-scheme корня (по умолчанию тёмная, data-theme
// light|system — явно). light-dark() принимает только цвета: у тени геометрия снаружи.
import { readFile, writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';

const HEADER = 'Сгенерировано из contracts/tokens/tokens.json — не править руками.';
const px = (group, prefix) => Object.entries(group).map(([k, v]) => `--wf-${prefix}-${k}: ${v}px;`);

// Сначала светлое: light-dark(<light>, <dark>) — порядок аргументов CSS.
const color = (v) => (typeof v === 'string' ? v : `light-dark(${v.light}, ${v.dark})`);

export function buildCss(t) {
  const vars = [
    ...Object.entries(t.color).map(([k, v]) => `--wf-color-${k}: ${color(v)};`),
    ...Object.entries(t.shadow).map(
      ([k, s]) => `--wf-shadow-${k}: 0 ${s.y}px ${s.blur}px ${color(s.color)};`,
    ),
    ...Object.entries(t.font).map(([k, v]) => `--wf-font-${k}: ${v};`),
    ...Object.entries(t.text).flatMap(([k, [size, line]]) => [
      `--wf-text-${k}: ${size}px;`,
      `--wf-text-${k}-line: ${line}px;`,
    ]),
    ...px(t.space, 'space'),
    ...px(t.radius, 'radius'),
    ...px(t.size, 'size'),
    // Ширины переключения раскладки (P21): в @media и @container переменные не работают — значение для
    // JS (matchMedia через tokens.ts) и для calc(); в CSS-запросах число пишется литералом (страж layout-css)
    ...px(t.breakpoint, 'breakpoint'),
    ...px(t.container, 'container'),
    ...Object.entries(t.density).map(([k, v]) => `--wf-density-${k}-row: ${v.row}px;`),
    ...Object.entries(t.duration).map(([k, v]) => `--wf-duration-${k}: ${v}ms;`),
    `--wf-ease: ${t.ease};`,
    `--wf-ease-exit: ${t['ease-exit']};`,
  ];
  const reduced = Object.keys(t.duration).map((k) => `    --wf-duration-${k}: 0ms;`);
  return [
    `/* ${HEADER} */`,
    ':root {',
    '  color-scheme: dark;',
    ...vars.map((v) => `  ${v}`),
    '}',
    "\n:root[data-theme='light'] {\n  color-scheme: light;\n}",
    "\n:root[data-theme='system'] {\n  color-scheme: light dark;\n}",
    '\n/* переходы на токенах выключаются сами; прочее движение гасит свой @media компонента */',
    '@media (prefers-reduced-motion: reduce) {',
    '  :root {',
    ...reduced,
    '  }',
    '}',
    '',
  ].join('\n');
}

export function buildTs(t) {
  return (
    `// ${HEADER}\nexport const tokens = ${JSON.stringify(t, null, 2)} as const;\n\n` +
    `export type ThemeName = 'light' | 'dark';\n`
  );
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const src = new URL('../../../contracts/tokens/tokens.json', import.meta.url);
  const tokens = JSON.parse(await readFile(src, 'utf8'));
  await writeFile(new URL('../tokens.css', import.meta.url), buildCss(tokens));
  await writeFile(new URL('../tokens.ts', import.meta.url), buildTs(tokens));
}
