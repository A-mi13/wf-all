import { useCallback, useMemo } from 'react';
import { useTranslations } from 'use-intl';
import type { ChipTone } from '../../ui';
import { AUDIT_SECTIONS, type AuditSection, SECTIONS } from './search';

// Подписи «Журнала действий»: действие (audit.actions.<код>), вид объекта (audit.objects.<вид>),
// тег раздела. Коды открытые (модуль новее сборки): действие или вид без подписи — сам код (его
// понимает поддержка), раздел без вкладки — «система».

const isLeaf = (v: unknown): v is string => typeof v === 'string';

// Коды действий, у которых есть подпись: { staff: { login: … } } → ['staff.login', …].
function codes(tree: unknown, prefix = ''): string[] {
  if (!tree || typeof tree !== 'object') return [];
  return Object.entries(tree).flatMap(([k, v]) =>
    isLeaf(v) ? [`${prefix}${k}`] : codes(v, `${prefix}${k}.`),
  );
}

export function useAuditLabels() {
  const t = useTranslations('audit');
  const action = useCallback(
    (code: string) => {
      const key = `actions.${code}`;
      return t.has(key) && isLeaf(t.raw(key)) ? t(key) : code;
    },
    [t],
  );
  const object = useCallback(
    (type: string) => {
      const key = `objects.${type}`;
      return t.has(key) && isLeaf(t.raw(key)) ? t(key) : type;
    },
    [t],
  );
  const knownActions = useMemo(() => codes(t.raw('actions')), [t]);
  const knownObjects = useMemo(() => codes(t.raw('objects')), [t]);
  return { action, object, knownActions, knownObjects };
}

// Раздел записи — по началу кода (те же префиксы, что у вкладок).
export function sectionOf(code: string): AuditSection | undefined {
  return SECTIONS.find((s) => code.startsWith(AUDIT_SECTIONS[s]));
}

// Отказы и блокировки — красным; решения подтверждений — акцентом; прочее — нейтрально.
export function tagTone(code: string): ChipTone {
  if (/_failed$|\.locked$/.test(code)) return 'danger';
  return sectionOf(code) === 'approvals' ? 'accent' : 'neutral';
}
