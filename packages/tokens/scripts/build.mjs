// tokens.json (contracts/) → tokens.css + tokens.ts. Источник один на веб, админку и позже Flutter.
import { readFile, writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';

const HEADER = 'Сгенерировано из contracts/tokens/tokens.json — не править руками.';
const px = (group, prefix) => Object.entries(group).map(([k, v]) => `--wf-${prefix}-${k}: ${v}px;`);

export function buildCss(t) {
  const vars = [
    ...Object.entries(t.color).map(([k, v]) => `--wf-color-${k}: ${v};`),
    ...Object.entries(t.font).map(([k, v]) => `--wf-font-${k}: ${v};`),
    ...Object.entries(t.text).flatMap(([k, [size, line]]) => [
      `--wf-text-${k}: ${size}px;`,
      `--wf-text-${k}-line: ${line}px;`,
    ]),
    ...px(t.space, 'space'),
    ...px(t.radius, 'radius'),
    ...px(t.size, 'size'),
    ...Object.entries(t.duration).map(([k, v]) => `--wf-duration-${k}: ${v}ms;`),
    `--wf-ease: ${t.ease};`,
  ];
  return `/* ${HEADER} */\n:root {\n  color-scheme: dark;\n${vars.map((v) => `  ${v}`).join('\n')}\n}\n`;
}

export function buildTs(t) {
  return `// ${HEADER}\nexport const tokens = ${JSON.stringify(t, null, 2)} as const;\n`;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const src = new URL('../../../contracts/tokens/tokens.json', import.meta.url);
  const tokens = JSON.parse(await readFile(src, 'utf8'));
  await writeFile(new URL('../tokens.css', import.meta.url), buildCss(tokens));
  await writeFile(new URL('../tokens.ts', import.meta.url), buildTs(tokens));
}
