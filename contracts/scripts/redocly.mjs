// Запуск redocly CLI — общий для scripts/bundle.mjs и тестов бандлов.
// CLI запускается текущим Node без оболочки: нет ветки под Windows и проблем с пробелами в пути.
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

// корень @wf/contracts: от него считаются пути openapi/…
const root = fileURLToPath(new URL('..', import.meta.url));
// bin.redocly пакета @redocly/cli (ESM)
const cli = fileURLToPath(new URL('../node_modules/@redocly/cli/bin/cli.js', import.meta.url));

// Собирает openapi/<name>/root.yaml в бандл: с out — в файл openapi/<name>.yaml рядом,
// без out — в stdout, бандл возвращается строкой.
export const buildBundle = (name, out) =>
  execFileSync(
    process.execPath,
    [cli, 'bundle', `openapi/${name}/root.yaml`, ...(out ? ['-o', out] : [])],
    {
      cwd: root,
      encoding: 'utf8',
      stdio: out ? 'inherit' : ['ignore', 'pipe', 'pipe'],
      // бандл в stdout растёт вместе с API; по умолчанию буфер 1 МиБ — больше обрывает сборку ENOBUFS
      maxBuffer: 64 * 1024 * 1024,
      // сборка герметична: без телеметрии (метаданные контракта наружу не уходят)
      // и без проверки обновлений CLI в реестре npm
      env: { ...process.env, REDOCLY_TELEMETRY: 'off', REDOCLY_SUPPRESS_UPDATE_NOTICE: 'true' },
    },
  );
