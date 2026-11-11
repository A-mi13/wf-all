import { useQueryClient } from '@tanstack/react-query';
import { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslations } from 'use-intl';
import { decide, type Me } from '../../app/permissions';
import { ME_KEY } from '../../app/query';
import { useSession } from '../../app/session';
import { Button } from '../Button';
import { useErrorText } from '../errorText';
import { ActionGate } from '../gate/ActionGate';
import { ConfirmDialog } from '../overlay';
import { useToast } from '../toast';
import { type BulkOptions, type BulkReport, retryBulk, runBulk } from './bulk';
import styles from './BulkBar.module.css';
import type { BulkAction } from './model';

// Панель «Выбрано N» (спека UI-фундамента §4.5): действия над выбранными строками, подтверждение с числом
// и обеими частями («Выключить 3; ещё 2 — без вашего права: пропустим»), итог по видам и «Повторить
// неудавшиеся». Право — на каждую строку и по её городу (permission(row) → { perm, cityId }): действие
// доступно, если разрешено хоть для одной; «Только с компьютера» — ActionGate по праву строк. Кнопки и
// подтверждение — по me сессии (подписка: права сменились — панель следует); исполнение — по me из
// кэша в момент запроса (после 403/401 me перечитывается, оставшиеся строки перепроверяются). Ушли со
// страницы или сменился сотрудник (размонтирование) — пачка отменяется: запросы в полёте доходят,
// новые не стартуют, итог — тостом. После новых 202 — onPending (экран передаёт
// useInvalidateApprovals()); после каждой пачки — onSettled(report) (экран перечитывает список один раз).
export type BulkBarProps<T> = {
  rows: readonly T[]; // выбранные строки, которые есть в данных
  rowLabel: (row: T) => string; // имя строки в списке ошибок итога
  actions: BulkAction<T>[];
  onClear: () => void;
  onPending: () => unknown;
  onSettled?: (report: BulkReport<T>) => unknown;
  // итог прошлой пачки без запуска — витрина и снимки (состояние «итог с ошибками»)
  preview?: { actionId: string; report: BulkReport<T> };
};

type Run<T> = { action: BulkAction<T>; report: BulkReport<T> };

export function BulkBar<T>({
  rows,
  rowLabel,
  actions,
  onClear,
  onPending,
  onSettled,
  preview,
}: BulkBarProps<T>) {
  const t = useTranslations('table.bulk');
  const errorText = useErrorText();
  const toast = useToast();
  const queryClient = useQueryClient();
  const session = useSession();
  const me = session.status === 'active' ? session.me : null;
  // действие диалога остаётся после закрытия: диалог смонтирован, фокус возвращается на кнопку
  const [dialog, setDialog] = useState<BulkAction<T> | null>(null);
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [progress, setProgress] = useState<{ done: number; total: number } | null>(null);
  const [last, setLast] = useState<Run<T> | null>(() => {
    const action = preview && actions.find((a) => a.id === preview.actionId);
    return action && preview ? { action, report: preview.report } : null;
  });
  // повторный вызов во время пачки (двойной клик, Enter дважды) — до перерисовки с busy
  const running = useRef(false);
  const abort = useRef<AbortController | null>(null);
  const mounted = useRef(false);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      abort.current?.abort();
    };
  }, []);

  // решение — по me из кэша в момент запроса, не по снимку рендера
  const decideNow = useCallback(
    (permission: string, cityId?: string) => {
      const current = queryClient.getQueryData<Me>(ME_KEY);
      return current?.stage === 'active' ? decide(current, permission, cityId) : 'deny';
    },
    [queryClient],
  );
  // для рендера — me сессии (подписка); permission(row) бросил — строка недоступна (bulk.ts — так же)
  const allowedPerm = (action: BulkAction<T>, row: T): string | null => {
    try {
      const { perm, cityId } = action.permission(row);
      return me && decide(me, perm, cityId) !== 'deny' ? perm : null;
    } catch {
      return null;
    }
  };
  const allowed = (action: BulkAction<T>) => rows.filter((r) => allowedPerm(action, r) !== null);

  const summary = (r: BulkReport<T>) =>
    [
      r.executed.length > 0 && t('executed', { count: r.executed.length }),
      r.pending.length > 0 && t('pending', { count: r.pending.length }),
      r.failed.length > 0 && t('failed', { count: r.failed.length }),
      r.unknown.length > 0 && t('unknown', { count: r.unknown.length }),
      r.skipped.length > 0 && t('skipped', { count: r.skipped.length }),
      r.cancelled.length > 0 && t('cancelled', { count: r.cancelled.length }),
    ]
      .filter(Boolean)
      .join(' · ');

  const start = async (
    action: BulkAction<T>,
    exec: (opts: BulkOptions) => Promise<BulkReport<T>>,
    pendingBefore = 0, // 202 прошлого итога (повтор переносит их): onPending — только на новые
  ) => {
    if (running.current) return;
    running.current = true;
    const ctl = new AbortController();
    abort.current = ctl;
    setBusy(true);
    setProgress(null);
    try {
      const report = await exec({
        decide: decideNow,
        signal: ctl.signal,
        onProgress: (done, total) => {
          if (mounted.current) setProgress({ done, total });
        },
        onForbidden: () =>
          queryClient.refetchQueries({ queryKey: ME_KEY, exact: true }, { cancelRefetch: false }),
      });
      if (report.pending.length > pendingBefore) void onPending();
      void onSettled?.(report);
      if (mounted.current) {
        setLast({ action, report });
      } else {
        const text = summary(report);
        if (report.failed.length + report.unknown.length > 0) toast.error(text);
        else toast.info(text);
      }
    } finally {
      running.current = false;
      if (abort.current === ctl) abort.current = null;
      if (mounted.current) {
        setBusy(false);
        setProgress(null);
        setOpen(false);
      }
    }
  };

  const confirmRows = dialog ? allowed(dialog) : [];
  const toRetry = last ? last.report.failed.length + last.report.unknown.length : 0;

  return (
    <section className={styles.bar} aria-label={t('region')} aria-busy={busy}>
      <div className={styles.row}>
        <span className={styles.count}>{t('selected', { count: rows.length })}</span>
        {actions.map((action) => {
          const perms = [
            ...new Set(rows.map((r) => allowedPerm(action, r)).filter((p) => p !== null)),
          ];
          return (
            <ActionGate key={action.id} permission={perms} compact>
              <Button
                variant={action.tone === 'danger' ? 'danger-outline' : 'secondary'}
                disabled={perms.length === 0}
                loading={busy}
                onClick={() => {
                  setDialog(action);
                  setOpen(true);
                }}
              >
                {action.label}
              </Button>
            </ActionGate>
          );
        })}
        <Button variant="link" onClick={onClear} disabled={busy}>
          {t('clear')}
        </Button>
      </div>
      {/* прогресс — без живой области (200 строк — не 200 объявлений), итог — в живой */}
      {progress && <div className={styles.report}>{t('progress', progress)}</div>}
      <div role="status" className={styles.report}>
        {last && !busy ? summary(last.report) : null}
      </div>
      {last && !busy && toRetry > 0 && (
        <>
          <ul className={styles.problems}>
            {last.report.failed.map(({ row, error }, i) => (
              <li key={`f${i}`}>
                {t('row_failed', { name: rowLabel(row), error: errorText(error) })}
              </li>
            ))}
            {last.report.unknown.map(({ row }, i) => (
              <li key={`u${i}`}>{t('row_unknown', { name: rowLabel(row) })}</li>
            ))}
          </ul>
          <div className={styles.row}>
            <Button
              variant="secondary"
              onClick={() => {
                const prev = last;
                void start(
                  prev.action,
                  (opts) => retryBulk(prev.report, prev.action, opts),
                  prev.report.pending.length,
                );
              }}
            >
              {t('retry_failed')}
            </Button>
          </div>
        </>
      )}
      {dialog && (
        <ConfirmDialog
          open={open}
          onClose={() => {
            if (!busy) setOpen(false);
          }}
          onConfirm={() => {
            void start(dialog, (opts) => runBulk(rows, dialog, opts));
          }}
          title={dialog.confirm.title(confirmRows.length)}
          text={
            rows.length > confirmRows.length
              ? t('skip_part', { count: rows.length - confirmRows.length })
              : undefined
          }
          confirmWord={dialog.confirm.word ?? String(confirmRows.length)}
          confirmLabel={dialog.label}
          loading={busy}
        />
      )}
    </section>
  );
}
