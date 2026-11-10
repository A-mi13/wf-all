// Эталон визуального сравнения админки (спека UI-фундамента §5.3, план UF3.5) — job `baseline` workflow
// .github/workflows/admin-visual.yml на раннере (там есть git и gh; в образе Playwright их может не быть, m-d).
//   node scripts/visual-baseline.mjs   — окружение GitHub Actions: GITHUB_EVENT_NAME, GITHUB_REPOSITORY, GITHUB_SHA,
//                                         GITHUB_OUTPUT, GITHUB_STEP_SUMMARY, GH_TOKEN; checkout с fetch-depth: 0.
// Выходы: base, changed, run-id, artifact, pr, pr-visual-change, image.
// - База — родитель merge-коммита (HEAD^1): pull_request.base.sha события может отстать от того, с чем слит PR;
//   на push в main — прошлое состояние main.
// - Эталон — первый успешный push-прогон этого workflow на main, чей коммит — предок базы и между ними нет
//   изменений визуальных путей (картинка та же, что у базы), с живым артефактом visual-baseline-<sha>.
// Эталона нет — пусто (снимки сравнение пропустят с предупреждением). Ошибка git/gh — падение job, а не «эталона
// нет»: иначе регрессия проходила бы молча. Функции — с внедряемым исполнителем команд (тест подменяет).
import { spawnSync } from 'node:child_process';
import { appendFileSync, existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';

// Один список визуальных путей (pathspec git): от них зависит вид админки и её снимков.
export const VISUAL_PATHS = [
  'apps/admin/src',
  ':(exclude,glob)apps/admin/src/**/*.test.*',
  'apps/admin/index.html',
  'apps/admin/public',
  'apps/admin/vite.config.ts',
  'apps/admin/vite.shots.ts',
  'apps/admin/package.json',
  'apps/admin/.ladle',
  'apps/admin/visual',
  'packages/tokens',
  'packages/icons',
  'contracts/tokens',
  'pnpm-lock.yaml',
  '.github/workflows/admin-visual.yml',
  'scripts/visual-baseline.mjs',
];

const ARTIFACT_PREFIX = 'visual-baseline-';

const must = (r, what) => {
  if (r.code !== 0) throw new Error(`${what}: код ${r.code}`);
  return r.stdout;
};

export function findBase(run) {
  const r = run('git', ['rev-parse', 'HEAD^1']);
  if (r.code !== 0) {
    throw new Error('git rev-parse HEAD^1 не удался: нужен actions/checkout с fetch-depth: 0');
  }
  return r.stdout.trim();
}

export function changed(run, base) {
  const out = must(
    run('git', ['diff', '--name-only', base, 'HEAD', '--', ...VISUAL_PATHS]),
    'git diff',
  );
  return out.trim() !== '';
}

export function findBaseline(run, base, accept = () => true) {
  const out = must(
    run('gh', [
      'run',
      'list',
      '--workflow',
      'admin-visual.yml',
      '--branch',
      'main',
      '--event',
      'push',
      '--status',
      'success',
      '--json',
      'databaseId,headSha',
      '--limit',
      '100',
    ]),
    'gh run list',
  );
  for (const { databaseId, headSha } of JSON.parse(out)) {
    // 1 — не предок; 128 — коммита нет в истории (force-push в main, чужой прогон): не эталон
    if (run('git', ['merge-base', '--is-ancestor', headSha, base]).code !== 0) continue;
    if (run('git', ['diff', '--quiet', headSha, base, '--', ...VISUAL_PATHS]).code !== 0) continue;
    const found = { runId: String(databaseId), sha: headSha };
    if (accept(found)) return found;
  }
  return null;
}

export function artifactAlive(run, repo, runId) {
  const out = must(
    run('gh', ['api', `repos/${repo}/actions/runs/${runId}/artifacts`]),
    'gh api artifacts',
  );
  const art = JSON.parse(out).artifacts.find(
    (a) => a.name.startsWith(ARTIFACT_PREFIX) && !a.expired,
  );
  return art ? art.name : null;
}

// Эталон с живым артефактом: истёкший — ищем следующий подходящий.
export function resolveBaseline(run, repo, base) {
  let artifact = null;
  const found = findBaseline(run, base, (c) => {
    artifact = artifactAlive(run, repo, c.runId);
    return artifact !== null;
  });
  return found && artifact ? { ...found, artifact } : null;
}

export function prOfCommit(run, repo, sha) {
  const out = must(run('gh', ['api', `repos/${repo}/commits/${sha}/pulls`]), 'gh api pulls');
  const pr = JSON.parse(out)[0];
  if (!pr) return null;
  return { number: pr.number, visualChange: pr.labels.some((l) => l.name === 'visual-change') };
}

export function playwrightImage(version) {
  if (!/^\d+\.\d+\.\d+$/.test(version)) {
    throw new Error(`@playwright/test ${version}: нужна точная версия — по ней тег образа`);
  }
  return `mcr.microsoft.com/playwright:v${version}-noble`;
}

// ——— job snapshots (в контейнере; без git и gh): режим сравнения и сводка ———

// meta — meta.json скачанного эталона (null — эталона нет). Информативно (отличия — в отчёт, без падения): на main
// (там снимок — следующий эталон), с меткой visual-change, при другом образе Playwright у эталона.
export function visualMode({ event, labeled, meta, image }) {
  const compare = meta !== null;
  const otherImage = compare && meta.image !== image;
  return {
    compare,
    informative: event === 'push' || labeled || otherImage,
    note: !compare
      ? 'Эталона нет: вид не сверен.'
      : otherImage
        ? `Образ Playwright эталона (${meta.image}) не совпадает с прогоном (${image}) — сравнение только информативно.`
        : '',
  };
}

// JSON-отчёт Playwright → отличия (аннотация visual-diff), новые снимки без эталона (visual-new), упавшие тесты.
export function collectResults(report) {
  const diffs = [];
  const missing = [];
  let failed = 0;
  const walk = (suite) => {
    for (const spec of suite.specs ?? []) {
      for (const t of spec.tests ?? []) {
        if (t.status === 'unexpected') failed++;
        for (const a of t.annotations ?? []) {
          if (a.type === 'visual-diff') diffs.push(a.description);
          if (a.type === 'visual-new') missing.push(a.description);
        }
      }
    }
    for (const s of suite.suites ?? []) walk(s);
  };
  for (const s of report.suites ?? []) walk(s);
  return { diffs, missing, failed };
}

export function summarize({ results, mode, event, pr, prVisualChange }) {
  const lines = ['### Визуальное сравнение — снимки', ''];
  if (mode.note) lines.push(mode.note, '');
  lines.push(
    `Упавших тестов (дымовой прогон или отличие без метки): ${results.failed}.`,
    `Отличий от эталона: ${results.diffs.length}; снимков без эталона: ${results.missing.length}.`,
  );
  if (results.diffs.length > 0) {
    lines.push('', ...results.diffs.slice(0, 50).map((d) => `- ${d}`));
    if (results.diffs.length > 50) lines.push(`- … и ещё ${results.diffs.length - 50}`);
    lines.push(
      '',
      'Было / стало / разница — артефакт отчёта этого прогона (visual-report-<run_id>).',
    );
  }
  const warnings = [];
  if (!mode.compare) warnings.push(['Эталона нет', mode.note]);
  if (event === 'push' && pr && !prVisualChange && results.diffs.length > 0) {
    warnings.push([
      'Вид изменён без метки',
      `PR #${pr}: ${results.diffs.length} отличий, отчёт — артефакт прогона`,
    ]);
  }
  return { markdown: `${lines.join('\n')}\n`, warnings };
}

const exec = (cmd, args) => {
  const r = spawnSync(cmd, args, { encoding: 'utf8' });
  if (r.error) throw r.error;
  return { code: r.status ?? 1, stdout: r.stdout ?? '' };
};

function main(env = process.env, run = exec) {
  const root = join(import.meta.dirname, '..');
  const pkg = JSON.parse(readFileSync(join(root, 'apps/admin/package.json'), 'utf8'));
  const image = playwrightImage(pkg.devDependencies['@playwright/test']);
  const repo = env.GITHUB_REPOSITORY;
  const push = env.GITHUB_EVENT_NAME === 'push';
  const base = findBase(run);
  // на main снимки всегда: они — следующий эталон
  const isChanged = push || changed(run, base);
  const baseline = isChanged ? resolveBaseline(run, repo, base) : null;
  const pr = push ? prOfCommit(run, repo, env.GITHUB_SHA) : null;
  const out = {
    base,
    changed: String(isChanged),
    'run-id': baseline?.runId ?? '',
    artifact: baseline?.artifact ?? '',
    pr: pr ? String(pr.number) : '',
    'pr-visual-change': String(pr?.visualChange ?? false),
    image,
  };
  if (env.GITHUB_OUTPUT) {
    appendFileSync(
      env.GITHUB_OUTPUT,
      Object.entries(out)
        .map(([k, v]) => `${k}=${v}\n`)
        .join(''),
    );
  }
  const summary = !isChanged
    ? `Визуальных изменений нет (база ${base.slice(0, 7)}) — сравнение не нужно.`
    : baseline
      ? `Эталон: прогон ${baseline.runId} (main ${baseline.sha.slice(0, 7)}), артефакт ${baseline.artifact}.`
      : `Эталона нет: вид не сверен (нет подходящего прогона main с живым артефактом для базы ${base.slice(0, 7)}).`;
  if (env.GITHUB_STEP_SUMMARY)
    appendFileSync(env.GITHUB_STEP_SUMMARY, `### Визуальное сравнение\n\n${summary}\n`);
  if (isChanged && !baseline) console.log(`::warning title=Эталона нет::${summary}`);
  console.log(JSON.stringify(out));
}

// Из apps/admin (job snapshots): `mode` — выходы compare/informative; `summary` — сводка, аннотации и meta.json
// нового эталона (на push).
const VISUAL_DIR = join(import.meta.dirname, '../apps/admin/visual');
const readJson = (f) => (existsSync(f) ? JSON.parse(readFileSync(f, 'utf8')) : null);

function modeOf(env) {
  return visualMode({
    event: env.EVENT,
    labeled: env.LABELED === 'true',
    meta: readJson(join(VISUAL_DIR, '.baseline/meta.json')),
    image: env.VISUAL_IMAGE,
  });
}

function modeCmd(env = process.env) {
  const m = modeOf(env);
  if (env.GITHUB_OUTPUT) {
    appendFileSync(env.GITHUB_OUTPUT, `compare=${m.compare}\ninformative=${m.informative}\n`);
  }
  console.log(JSON.stringify(m));
}

function summaryCmd(env = process.env) {
  const mode = modeOf(env);
  const report = readJson(join(VISUAL_DIR, 'results.json'));
  const results = report ? collectResults(report) : { diffs: [], missing: [], failed: 0 };
  const { markdown, warnings } = summarize({
    results,
    mode,
    event: env.EVENT,
    pr: env.PR,
    prVisualChange: env.PR_VISUAL_CHANGE === 'true',
  });
  if (env.GITHUB_STEP_SUMMARY) appendFileSync(env.GITHUB_STEP_SUMMARY, markdown);
  for (const [title, text] of warnings) console.log(`::warning title=${title}::${text}`);
  if (!report)
    console.log('::warning title=Нет отчёта::visual/results.json не найден — снимки не прошли');
  if (env.EVENT === 'push') {
    mkdirSync(join(VISUAL_DIR, '.out'), { recursive: true });
    writeFileSync(
      join(VISUAL_DIR, '.out/meta.json'),
      `${JSON.stringify({ image: env.VISUAL_IMAGE, sha: env.GITHUB_SHA }, null, 2)}\n`,
    );
  }
  process.stdout.write(markdown);
}

if (import.meta.url === pathToFileURL(process.argv[1] ?? '').href) {
  const cmd = process.argv[2];
  if (cmd === 'mode') modeCmd();
  else if (cmd === 'summary') summaryCmd();
  else main();
}
