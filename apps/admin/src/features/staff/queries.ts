import { useQuery } from '@tanstack/react-query';
import { useCallback } from 'react';
import { useTranslations } from 'use-intl';
import { unwrap } from '../../api/client';
import { useApi } from '../../api/context';

// Ключи «Ролей и доступа». Всё про учётки — под ACCOUNTS_KEY (список, карточка, сеансы цели): после
// мутации над сотрудником одна инвалидация перечитывает и список, и открытую панель.
export const ROLES_KEY = ['staff', 'roles'] as const;
export const ACCOUNTS_KEY = ['staff', 'accounts'] as const;
export const accountKey = (id: string) => [...ACCOUNTS_KEY, 'card', id] as const;
export const accountSessionsKey = (id: string) => [...ACCOUNTS_KEY, 'sessions', id] as const;

// Матрица ролей — одна на экран (матрица и «Правило двух рук» читают один запрос).
export function useStaffRoles() {
  const api = useApi();
  return useQuery({
    queryKey: ROLES_KEY,
    queryFn: () => unwrap(api.GET('/v1/staff/roles')),
    staleTime: 10 * 60_000,
  });
}

// Подпись роли из локалей; незнакомая роль (enum открытый) — «сотрудник».
export function useRoleLabel(): (role: string) => string {
  const t = useTranslations('roles');
  return useCallback((role) => (t.has(role) ? t(role) : t('unknown')), [t]);
}

// Подпись права из локалей; ключа без подписи (право новее сборки) — сам ключ.
export function usePermissionLabel(): (key: string) => string {
  const t = useTranslations('permissions');
  return useCallback((key) => (t.has(key) && typeof t.raw(key) === 'string' ? t(key) : key), [t]);
}
