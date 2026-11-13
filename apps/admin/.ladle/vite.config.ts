import { assetsInlineLimit, CSS_TARGETS } from '../vite.config.ts';

// Конфиг Vite для Ladle 5.1.1 — внутри него Vite 6 (проба UF3.1). Свой, а не vite.config.ts админки:
// - тот подключает @vitejs/plugin-react 6 (peer — Vite 8): React-плагин Ладла (@vitejs/plugin-react-swc)
//   ставится сам, когда в конфиге нет своего;
// - свой каталог кэша: две разные Vite в общий node_modules/.vite (dev-сервер админки — Vite 8) затирали бы
//   друг другу предсборку зависимостей (и EPERM на Windows, как у e2e/vite.config.ts);
// - без dev-прокси /v1 и порта 5173: истории src/ui без API, порт — у Ладла (61000).
// Сборка — как у админки: те же минимальные браузеры (light-dark() без полифила) и шрифты файлами.
export default {
  cacheDir: 'node_modules/.vite-ladle',
  // public/ админки (theme-init.js) витрине не нужен: тему ставит Ладл и обёртка
  publicDir: false,
  build: {
    cssTarget: [...CSS_TARGETS],
    assetsInlineLimit,
  },
};
