---
paths:
  - "contracts/**"
---

# Контракты

- Изменил YAML → `./task gen` → закоммить сгенерированное в `backend/` и `apps/`.
- Схема `Problem` правится в `public.yaml` и `admin.yaml` одинаково (тест `contracts/test/contracts.test.mjs`).
- Ломающее изменение `public.yaml` — только с новым major тега `contracts-vX.Y.Z`.
- Изменил `tokens/tokens.json` → `pnpm --filter @wf/tokens build` (входит в `./task gen`).
