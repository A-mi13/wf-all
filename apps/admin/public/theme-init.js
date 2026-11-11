// Тема до первого кадра (спека UI-фундамента §4.12). Обычный (не модульный) скрипт в <head> до стилей:
// читает выбор localStorage['wf.theme'] и ставит <html data-theme>. Отдельный файл, а не инлайн: CSP admin-api —
// script-src 'self'. Vite копирует public/ как есть — синтаксис ES5 (кроме `catch {}` без
// привязки — ES2019, есть во всех минимальных браузерах: Chrome 123, Firefox 125, Safari 17.5), без сборки. Ошибка доступа к storage
// (приватный режим) — тема по системе. Правила разбора те же, что у parseTheme в src/ui/theme/ThemeProvider.tsx
// (страж — src/ui/theme/theme-init.test.ts). Явный выбор сразу красит и обе метки theme-color (панель браузера
// без мигания до React, ревью UF1b/II m6); BG — tokens.color.bg (сверяет тот же страж).
(function () {
  function parse(raw) {
    return raw === 'light' || raw === 'dark' || raw === 'system' ? raw : 'system';
  }
  var BG = { dark: '#0B0F0D', light: '#F3F6F3' };
  window.__wfTheme = { parse: parse, bg: BG };
  var raw = null;
  try {
    raw = window.localStorage.getItem('wf.theme');
  } catch {
    raw = null;
  }
  var theme = parse(raw);
  document.documentElement.dataset.theme = theme;
  if (theme !== 'system') {
    var metas = document.querySelectorAll('meta[name="theme-color"]');
    for (var i = 0; i < metas.length; i++) metas[i].setAttribute('content', BG[theme]);
  }
})();
