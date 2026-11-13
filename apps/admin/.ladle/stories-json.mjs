// Список историй для снимков CI (план UF3.2): после `ladle build` — <outDir>/stories.json
// [{ id, title, visual, layout }] из meta.json Ладла и меток в исходниках (src/showcase/storyList.ts).
// Запуск — из apps/admin: node .ladle/stories-json.mjs [каталог сборки, по умолчанию dist-stories].
import { readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { storyList } from '../src/showcase/storyList.ts';

const dir = process.argv[2] ?? 'dist-stories';
const meta = JSON.parse(readFileSync(join(dir, 'meta.json'), 'utf8'));
const list = storyList(meta, (file) => readFileSync(file, 'utf8'));
writeFileSync(join(dir, 'stories.json'), `${JSON.stringify(list, null, 2)}\n`);
const visual = list.filter((s) => s.visual);
console.log(
  `stories.json: историй ${list.length}, visual ${visual.length} (из них layout ${visual.filter((s) => s.layout).length})`,
);
