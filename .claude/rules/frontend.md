---
paths:
  - "apps/**"
  - "packages/**"
---

# Фронты

- Приложения не импортируют друг друга; общее — только `@wf/*` из `packages/` (eslint-plugin-boundaries). Клиент админского API — только в `apps/admin`.
- Клиент API — через `ApiProvider`/`useApi`; адрес — тот же origin (`/v1` проксирует dev-сервер: rewrites в `next.config.ts`, `server.proxy` в `vite.config.ts`).
- Цвета, отступы, шрифты — только CSS-переменные `--wf-*` из `@wf/tokens`; `tokens.css` сгенерирован, руками не править.
- Строки в компонентах — только через `useTranslations`; новый ключ — сразу в `ru.json` и `en.json`.
- Тест компонента — `renderWithProviders` (`src/test/render.tsx`) + MSW-моки из `src/test/server.ts`.
- Async Server Components Next — через Playwright (`apps/web/e2e`), Vitest их не рендерит.
- Экран по макету: исходник в `design/screens/NN-*.dc.html`, токены — `design/03-токены.md`.
- Next 16 ≠ знакомый Next: перед кодом веба читать гайд в `apps/web/node_modules/next/dist/docs/` (подгружается из `apps/web/CLAUDE.md` → `@AGENTS.md`).
