import { useQuery } from '@tanstack/react-query';
import { unwrap } from '../../api/client';
import { useApi } from '../../api/context';

// Запросы «Моего доступа». Ключи — не под ['staff', 'me']: тот префикс — сессия (ME_KEY), и её
// перечитывание после 401 не должно задевать эти списки.
export const MY_MFA_KEY = ['staff', 'my', 'mfa'] as const;
export const MY_SESSIONS_KEY = ['staff', 'my', 'sessions'] as const;

// Статус второго фактора: когда подключено приложение и сколько резервных кодов осталось.
export function useMyMfa() {
  const api = useApi();
  return useQuery({
    queryKey: MY_MFA_KEY,
    queryFn: () => unwrap(api.GET('/v1/staff/me/mfa')),
  });
}

export function useMySessions() {
  const api = useApi();
  return useQuery({
    queryKey: MY_SESSIONS_KEY,
    queryFn: () => unwrap(api.GET('/v1/staff/me/sessions')),
  });
}
