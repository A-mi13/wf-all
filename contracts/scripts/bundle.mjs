// Собирает бандлы контрактов из исходников по модулям: openapi/<имя>/root.yaml → openapi/<имя>.yaml.
// Бандл коммитится и руками не правится; свежесть сверяют тест бандлов в contracts/
// и backend:gen:check в CI.
import { buildBundle } from './redocly.mjs';

for (const name of ['public', 'admin']) {
  buildBundle(name, `openapi/${name}.yaml`);
}
