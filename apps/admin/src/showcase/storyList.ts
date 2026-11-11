import ts from 'typescript';

// Список историй для снимков (спека UI-фундамента §5.2–§5.3, план UF3.2): { id, title, visual, layout }[].
// id и файл каждой истории — из meta.json Ладла (`ladle build`), метки — из исходника: Ладл читает из
// default-экспорта только title и meta, а форма csf.ts (заморожена, m16) кладёт visual/layout на верхний
// уровень (проба UF3.1, п. 7). Метки истории (`X.meta = { … }`) важнее меток файла. Метка — только литерал
// true/false: иначе ошибка с файлом, а не тихое false (история выпала бы из сравнения незаметно).
// Модуль без DOM и Vite — его зовут и Vitest, и Node (.ladle/stories-json.mjs, Playwright CI).

export type StoryEntry = { id: string; title: string; visual: boolean; layout: boolean };

export type LadleMeta = {
  stories: Record<string, { name: string; filePath: string; namedExport: string }>;
};

type Flags = { title?: string; visual?: boolean; layout?: boolean };

const FLAG_KEYS = new Set(['title', 'visual', 'layout']);

// Имя свойства в любой записи (`visual`, `'visual'`, `['visual']`) — чтобы поймать метку не в той форме.
function keyOf(name: ts.PropertyName | undefined): string | null {
  if (!name) return null;
  if (ts.isIdentifier(name) || ts.isStringLiteralLike(name)) return name.text;
  if (ts.isComputedPropertyName(name) && ts.isStringLiteralLike(name.expression)) {
    return name.expression.text;
  }
  return null;
}

function readFlags(obj: ts.ObjectLiteralExpression, file: string, where: string): Flags {
  const flags: Flags = {};
  for (const prop of obj.properties) {
    // spread мог бы принести метку незаметно (ревью UF3, m2)
    if (ts.isSpreadAssignment(prop)) throw new Error(`${file}: ${where} — spread в объекте меток`);
    const named = keyOf(prop.name);
    if (named === null || !FLAG_KEYS.has(named)) continue;
    if (!ts.isPropertyAssignment(prop) || !ts.isIdentifier(prop.name)) {
      throw new Error(`${file}: ${where}.${named} — только запись «${named}: литерал»`);
    }
    const key = named;
    const value = prop.initializer;
    if (key === 'title') {
      if (!ts.isStringLiteralLike(value))
        throw new Error(`${file}: ${where}.title — не строковый литерал`);
      flags.title = value.text;
    } else if (key === 'visual' || key === 'layout') {
      if (value.kind === ts.SyntaxKind.TrueKeyword) flags[key] = true;
      else if (value.kind === ts.SyntaxKind.FalseKeyword) flags[key] = false;
      else throw new Error(`${file}: ${where}.${key} — только литерал true или false`);
    }
  }
  return flags;
}

// `{…} satisfies StoryMeta` / `{…} as …` / `({…})` → сам объект
function objectOf(expr: ts.Expression): ts.ObjectLiteralExpression | null {
  let e = expr;
  while (ts.isSatisfiesExpression(e) || ts.isAsExpression(e) || ts.isParenthesizedExpression(e)) {
    e = e.expression;
  }
  return ts.isObjectLiteralExpression(e) ? e : null;
}

function parseFile(source: string, file: string) {
  const sf = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, false, ts.ScriptKind.TSX);
  let fileFlags: Flags | null = null;
  const storyFlags = new Map<string, Flags>();
  for (const st of sf.statements) {
    if (ts.isExportAssignment(st) && !st.isExportEquals) {
      const obj = objectOf(st.expression);
      if (obj) fileFlags = readFlags(obj, file, 'default');
    } else if (
      ts.isExpressionStatement(st) &&
      ts.isBinaryExpression(st.expression) &&
      st.expression.operatorToken.kind === ts.SyntaxKind.EqualsToken &&
      ts.isPropertyAccessExpression(st.expression.left) &&
      st.expression.left.name.text === 'meta' &&
      ts.isIdentifier(st.expression.left.expression)
    ) {
      const story = st.expression.left.expression.text;
      const obj = objectOf(st.expression.right);
      if (!obj) throw new Error(`${file}: ${story}.meta — не объектный литерал`);
      storyFlags.set(story, readFlags(obj, file, `${story}.meta`));
    }
  }
  if (!fileFlags?.title)
    throw new Error(`${file}: нет default-экспорта { title: '…' } satisfies StoryMeta`);
  return { fileFlags, storyFlags };
}

export function storyList(meta: LadleMeta, read: (file: string) => string): StoryEntry[] {
  const files = new Map<string, ReturnType<typeof parseFile>>();
  const out: StoryEntry[] = [];
  for (const [id, story] of Object.entries(meta.stories)) {
    let parsed = files.get(story.filePath);
    if (!parsed) {
      parsed = parseFile(read(story.filePath), story.filePath);
      files.set(story.filePath, parsed);
    }
    const own = parsed.storyFlags.get(story.namedExport) ?? {};
    const title = parsed.fileFlags.title ?? '';
    out.push({
      id,
      title,
      visual: own.visual ?? parsed.fileFlags.visual ?? false,
      layout: own.layout ?? parsed.fileFlags.layout ?? false,
    });
  }
  return out.sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
}
