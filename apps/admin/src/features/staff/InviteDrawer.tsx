import { useIsMutating } from '@tanstack/react-query';
import { useEffect, useState } from 'react';
import { useForm, useWatch } from 'react-hook-form';
import { useTranslations } from 'use-intl';
import { type ApiError, unwrap } from '../../api/client';
import { useApi } from '../../api/context';
import { useAdminMutation } from '../../api/mutation';
import { useDraft } from '../../app/draft';
import { paths } from '../../app/paths';
import type { Me } from '../../app/permissions';
import {
  Button,
  Drawer,
  Form,
  PendingApprovalNotice,
  SelectField,
  TextField,
  useErrorText,
} from '../../ui';
import { useInvalidateApprovals } from '../approvals/queries';
import { CityField } from './CityField';
import { useRoleLabel } from './queries';
import { grantableRoles, ROLES } from './roles';
import styles from './staff.module.css';

type Values = { email: string; display_name: string; role: string; city_ids: string[] };

const EMPTY: Values = { email: '', display_name: '', role: '', city_ids: [] };

// Черновик формы приглашения (P19): почта, имя, роль, города — секретов в форме нет. Пишется
// ТОЛЬКО по вводу (событие change), не при размонтировании: иначе форма, закрытая выходом, вернула
// бы черновик в sessionStorage уже после clearDrafts() — следующему сотруднику за этим компьютером.
export const INVITE_DRAFT = `${paths.staff}:invite`;

const INVITE_KEY = ['staff', 'invite'] as const;

// «Пригласить» (спека §9.4 п. 5, §9.3, §4.9; форма в панели — *): почта, имя, роль — только
// выдаваемые (grant / grant_elevated из me.permissions), города модератора — поиск по
// listGeoCities, обязательны для модератора (проверка на клиенте до запроса). Приглашение всегда
// подтверждает второй сотрудник: 202 → «Отправлено на подтверждение», черновик стёрт.
// 409 staff.email_taken — на поле почты, 422 staff.city_not_found — на поле городов,
// validation.failed — по полям, остальное — общим текстом.
export function InviteDrawer({
  me,
  open,
  onClose,
}: {
  me: Me;
  open: boolean;
  onClose: () => void;
}) {
  const t = useTranslations('staff.invite');
  // Пока запрос идёт, панель не закрывается: ответ 202 должен дойти до экрана.
  const busy = useIsMutating({ mutationKey: INVITE_KEY }) > 0;
  return (
    <Drawer open={open} onClose={onClose} title={t('title')} dismissible={!busy}>
      <InviteBody me={me} onClose={onClose} />
    </Drawer>
  );
}

function InviteBody({ me, onClose }: { me: Me; onClose: () => void }) {
  const t = useTranslations('staff.invite');
  const api = useApi();
  const errorText = useErrorText();
  const roleLabel = useRoleLabel();
  // черновик — этого сотрудника: после истечения сессии другой за компьютером его не увидит
  const [draft, saveDraft, clearDraft] = useDraft<Partial<Values>>(INVITE_DRAFT, me.id ?? '');
  const roles = grantableRoles(me);
  // Роль из черновика, которую сотрудник больше выдать не может (права сменились), не подставляется.
  const [initial] = useState<Values>(() => ({
    ...EMPTY,
    ...draft,
    role: roles.find((r) => r === draft?.role) ?? '',
  }));
  const form = useForm<Values>({ defaultValues: initial });
  const role = useWatch({ control: form.control, name: 'role' });
  const [sent, setSent] = useState(false);
  const invalidateApprovals = useInvalidateApprovals();

  // Только ввод сотрудника (type === 'change'): ни монтирование, ни reset, ни размонтирование.
  useEffect(
    () =>
      form.subscribe({
        formState: { values: true },
        callback: ({ values, type }) => {
          if (type === 'change') saveDraft(values);
        },
      }),
    [form, saveDraft],
  );

  const invite = useAdminMutation(
    async (body: Values, key: string) => {
      const r = ROLES.find((x) => x === body.role);
      if (!r) throw new Error('InviteDrawer: роль не выбрана');
      return unwrap(
        api.POST('/v1/staff/invites', {
          params: { header: { 'Idempotency-Key': key } },
          body: {
            email: body.email,
            display_name: body.display_name,
            role: r,
            // города — только у модератора; у остальных ролей поля нет
            ...(r === 'moderator' ? { city_ids: body.city_ids } : {}),
          },
        }),
      );
    },
    {
      mutationKey: INVITE_KEY,
      onSuccess: () => {
        clearDraft();
        setSent(true);
        // 202 — новый запрос: счётчик и списки подтверждений перечитываются сразу
        void invalidateApprovals();
      },
    },
  );

  const onError = (e: ApiError) => {
    if (e.code === 'staff.email_taken') {
      form.setError('email', { type: 'server', message: errorText(e) }, { shouldFocus: true });
      return true;
    }
    if (e.code === 'staff.city_not_found') {
      form.setError('city_ids', { type: 'server', message: errorText(e) }, { shouldFocus: true });
      return true;
    }
    return false;
  };

  if (sent)
    return (
      <div className={styles.panel}>
        <PendingApprovalNotice />
        <div className={styles.row}>
          <Button
            variant="secondary"
            onClick={() => {
              form.reset(EMPTY);
              setSent(false);
            }}
          >
            {t('again')}
          </Button>
          <Button onClick={onClose}>{t('close')}</Button>
        </div>
      </div>
    );

  return (
    <div className={styles.panel}>
      <p className={styles.text}>{t('text')}</p>
      <Form form={form} onSubmit={(v) => invite.mutateAsync(v)} onError={onError}>
        <TextField
          name="email"
          type="email"
          label={t('email')}
          autoComplete="off"
          spellCheck={false}
          autoCapitalize="none"
          autoFocus
          rules={{ required: true }}
        />
        <TextField
          name="display_name"
          label={t('name')}
          hint={t('name_hint')}
          autoComplete="off"
          rules={{ required: true }}
        />
        <SelectField
          name="role"
          label={t('role')}
          placeholder={t('role_placeholder')}
          options={roles.map((r) => ({ value: r, label: roleLabel(r) }))}
          rules={{ required: true }}
        />
        {role === 'moderator' && (
          <CityField
            name="city_ids"
            label={t('cities')}
            hint={t('cities_hint')}
            searchPlaceholder={t('cities_search')}
            rules={{
              validate: {
                // модератор без городов — ошибка поля до запроса (сервер ответил бы min_items)
                min_items: (v: unknown) =>
                  form.getValues('role') !== 'moderator' || (Array.isArray(v) && v.length > 0),
              },
            }}
          />
        )}
        <Button type="submit" loading={invite.isPending}>
          {t('submit')}
        </Button>
      </Form>
    </div>
  );
}
