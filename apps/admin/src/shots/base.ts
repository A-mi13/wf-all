// Режим shots: база моков — сам dev-сервер (тот же origin, сервис-воркер MSW перехватывает /v1/*).
// Подменяет src/test/base.ts (плагин wf-shots, vite.shots.ts).
export const TEST_API = location.origin;
