// База API моков — единственное место (спека §5.4): тесты ходят на http://admin.test, режим shots
// подменяет этот модуль на src/shots/base.ts (location.origin) плагином wf-shots (vite.shots.ts).
export const TEST_API = 'http://admin.test';
