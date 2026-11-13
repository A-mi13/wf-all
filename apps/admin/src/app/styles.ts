// Глобальные стили админки — единственный модуль (план UF1a.1, m3): токены, шрифты и ui/global.css
// (U24: правила корня документа). Его импортируют и src/main.tsx, и точка входа режима shots (src/shots/main.tsx):
// снимки видят те же стили, что сборка.
import '@wf/tokens/tokens.css';
import './fonts';
import '../ui/global.css';
