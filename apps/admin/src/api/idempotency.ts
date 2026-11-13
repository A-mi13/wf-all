// Ключ идемпотентности (спека сотрудников §9.1, M6): один генератор на всю админку — одиночные мутации
// (useAdminMutation, один ключ на действие) и массовые действия (runBulk, ключ на строку на действие).
export const newIdempotencyKey = (): string => crypto.randomUUID();
