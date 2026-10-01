// Собирает бандлы контрактов из исходников по модулям: openapi/<имя>/root.yaml → openapi/<имя>.yaml.
// Бандл коммитится и руками не правится; backend:gen:check в CI сверяет его с исходниками.
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('..', import.meta.url));
const win = process.platform === 'win32';
const redocly = fileURLToPath(
  new URL(`../node_modules/.bin/redocly${win ? '.cmd' : ''}`, import.meta.url),
);

for (const name of ['public', 'admin']) {
  execFileSync(redocly, ['bundle', `openapi/${name}/root.yaml`, '-o', `openapi/${name}.yaml`], {
    cwd: root,
    stdio: 'inherit',
    // телеметрию CLI выключаем: метаданные контракта наружу не уходят
    env: { ...process.env, REDOCLY_TELEMETRY: 'off' },
    // .cmd на Windows запускается только через оболочку
    shell: win,
  });
}
