// Тема до первого кадра (спека UI-фундамента §4.12). Обычный (не модульный) скрипт в <head> до стилей:
// читает выбор localStorage['wf.theme'] и ставит <html data-theme>. Отдельный файл, а не инлайн: CSP admin-api —
// script-src 'self'. Vite копирует public/ как есть — синтаксис ES5, без сборки. Ошибка доступа к storage
// (приватный режим) — тема по системе. Правила разбора те же, что у parseTheme в src/ui/theme/ThemeProvider.tsx
// (страж — src/ui/theme/theme-init.test.ts).
(function () {
  function parse(raw) {
    return raw === 'light' || raw === 'dark' || raw === 'system' ? raw : 'system';
  }
  window.__wfTheme = { parse: parse };
  var raw = null;
  try {
    raw = window.localStorage.getItem('wf.theme');
  } catch (e) {
    raw = null;
  }
  document.documentElement.dataset.theme = parse(raw);
})();
