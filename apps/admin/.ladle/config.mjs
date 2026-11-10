// Витрина компонентов админки — Ladle 5.1.1 (спека UI-фундамента §5.1–§5.2, план UF3). Истории — CSF на
// своих типах (src/showcase/csf.ts), рядом с компонентами src/ui.
/** @type {import('@ladle/react').UserConfig} */
export default {
  stories: 'src/ui/**/*.stories.tsx',
  // Свой конфиг Vite для Ladle: vite.config.ts админки написан под Vite 8 (@vitejs/plugin-react 6,
  // build.cssTarget, test), а внутри Ladle 5.1.1 — Vite 6 (проба UF3.1).
  viteConfig: '.ladle/vite.config.ts',
  outDir: 'dist-stories',
  addons: {
    // тема — наш атрибут data-theme на <html>: Ladle ставит тот же атрибут, обёртка (components.tsx)
    // синхронизирует ThemeProvider; по умолчанию — тёмная, как админка без выбора
    theme: { enabled: true, defaultState: 'dark' },
    // истории src/ui без API: msw Ладла (2.x) не нужен и с нашим (3.x) не пересекается
    msw: { enabled: false },
    a11y: { enabled: false },
    rtl: { enabled: false },
    source: { enabled: false },
  },
};
