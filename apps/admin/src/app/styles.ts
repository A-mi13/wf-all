// Глобальные стили админки — единственный модуль (план UF1a.1, m3): токены и шрифты (с UF2a.2 —
// ui/global.css). Его импортируют и src/main.tsx, и точка входа режима shots (src/shots/main.tsx):
// снимки видят те же стили, что сборка.
import '@wf/tokens/tokens.css';
import './fonts';
