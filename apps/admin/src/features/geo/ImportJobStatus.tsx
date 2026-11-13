import { type Query, useQueryClient } from '@tanstack/react-query';
import { useEffect, useRef, useState } from 'react';
import { useTranslations } from 'use-intl';
import { type ApiError, isApiError } from '../../api/client';
import type { components } from '../../api/gen/admin';
import {
  Button,
  CopyButton,
  DataState,
  FormError,
  RelativeTime,
  Skeleton,
  StatusChip,
  useToast,
} from '../../ui';
import {
  capped,
  JOB_TONES,
  knownJobState,
  POLL_CEILING_MS,
  pollInterval,
  TERMINAL,
} from './importPoll';
import styles from './imports.module.css';
import { useCancelImportJob } from './importMutations';
import { IMPORTS_KEY, useGeoImportJob } from './queries';

type GeoImportJob = components['schemas']['GeoImportJob'];

// Ошибка опроса, после которой спрашивать дальше бессмысленно: 4xx — решение сервера (404 — задачи нет, 403 — прав
// нет), кроме 429. 5xx, сеть и 429 — временные: интервал идёт дальше, потолок queued его всё равно ограничит (ревью I-1).
function final(error: unknown): boolean {
  return isApiError(error) && error.status >= 400 && error.status < 500 && error.status !== 429;
}

// Строка задачи импорта над журналом (?job=<id>): состояние чипом, id — CopyButton, когда поставлена, попытка;
// «Отменить» — только queued и с geo.imports.run; конечное — журнал перечитан, есть строка журнала — «Открыть
// отчёт». 404 (River удалил задачу) — текст и журнал, «Скрыть задачу» убирает строку из адреса.
export function ImportJobStatus({
  jobId,
  canRun,
  onOpenReport,
  onHide,
}: {
  jobId: number;
  canRun: boolean;
  onOpenReport: (importId: string) => void;
  onHide: () => void;
}) {
  const t = useTranslations('geo.imports.job');
  const queryClient = useQueryClient();
  // монтирование строки (key={jobId} у экрана): от него считается окно опроса
  const [since] = useState(() => Date.now());
  const job = useGeoImportJob(jobId, (q: Query<GeoImportJob, ApiError>) =>
    final(q.state.error) ? false : pollInterval(q.state.data?.state, Date.now() - since),
  );
  const titleRef = useRef<HTMLSpanElement>(null);
  const state = job.data?.state;
  const terminal = state !== undefined && TERMINAL.includes(state);
  const gone = isApiError(job.error, 'geo.import_job_not_found');
  // опрос упал временно (5xx, сеть, 429 — после повторов клиента), данные прежние: строка не говорит «обновляется
  // сам», а предлагает «Проверить ещё раз»; интервал при этом идёт дальше (ревью I-1)
  const failed = job.isError && !gone;

  // конечное состояние или задачи нет — журнал перечитывается (строка импорта появилась или изменилась)
  useEffect(() => {
    if (terminal || gone)
      void queryClient.invalidateQueries({ queryKey: [...IMPORTS_KEY, 'list'] });
  }, [terminal, gone, queryClient]);

  return (
    <section className={styles.jobRow} aria-label={t('label')}>
      <div className={styles.jobHead}>
        {/* tabIndex -1: сюда уходит фокус, когда пропала нажатая «Отменить» (WCAG 2.4.3, ревью m-4) */}
        <span ref={titleRef} tabIndex={-1} className={styles.name}>
          {t('title', { id: jobId })}
        </span>
        <CopyButton value={String(jobId)} label={t('copy')} />
        {/* смена состояния слышна читалке; после 404 прежнего чипа нет (ревью m-3) */}
        <span role="status">{job.data && !gone && <JobChip state={job.data.state} />}</span>
      </div>
      {gone ? (
        <>
          <p className={styles.text} role="status">
            {t('not_found')}
          </p>
          <div className={styles.actions}>
            <Button variant="secondary" onClick={onHide}>
              {t('hide')}
            </Button>
          </div>
        </>
      ) : (
        <DataState query={job} scope="block" skeleton={<Skeleton.Text lines={2} />}>
          {(data) => (
            <JobBody
              job={data}
              stopped={
                capped(data.state) && job.dataUpdatedAt - since >= POLL_CEILING_MS && !terminal
              }
              failed={failed}
              checking={job.isFetching}
              onCancelled={() => titleRef.current?.focus()}
              onCheck={() => void job.refetch()}
              canRun={canRun}
              onOpenReport={onOpenReport}
              onHide={onHide}
            />
          )}
        </DataState>
      )}
    </section>
  );
}

// Подписи — литералами (страж неиспользуемых ключей видит только их); незнакомое состояние — «—» (m-8).
function JobChip({ state }: { state: string }) {
  const t = useTranslations('geo.imports.job.states');
  const text: Record<string, string> = {
    queued: t('queued'),
    running: t('running'),
    completed: t('completed'),
    failed: t('failed'),
    cancelled: t('cancelled'),
  };
  const tone = knownJobState(state) ? (JOB_TONES[state] ?? 'neutral') : 'neutral';
  return (
    <StatusChip tone={tone}>{(knownJobState(state) && text[state]) || t('unknown')}</StatusChip>
  );
}

function JobBody({
  job,
  stopped,
  failed,
  onCancelled,
  checking,
  onCheck,
  canRun,
  onOpenReport,
  onHide,
}: {
  job: GeoImportJob;
  stopped: boolean;
  failed: boolean;
  onCancelled: () => void;
  checking: boolean;
  onCheck: () => void;
  canRun: boolean;
  onOpenReport: (importId: string) => void;
  onHide: () => void;
}) {
  const t = useTranslations('geo.imports.job');
  const ta = useTranslations('actions');
  const toast = useToast();
  const cancel = useCancelImportJob(job.job_id);
  const notCancellable = isApiError(cancel.error, 'geo.import_job_not_cancellable');
  const importId = job.import_id;
  return (
    <div className={styles.jobBody}>
      {/* div, не p: подсказка RelativeTime — блочный Popover */}
      <div className={styles.muted}>
        {t.rich('created', { time: () => <RelativeTime value={job.created_at} /> })}
        {job.attempt > 0 && <> · {t('attempt', { attempt: job.attempt, max: job.max_attempts })}</>}
      </div>
      {stopped ? (
        <p className={styles.text} role="status">
          {job.state === 'queued' ? t('waiting_worker') : t('stalled')}
        </p>
      ) : failed ? (
        <p className={styles.text} role="status">
          {t('stale')}
        </p>
      ) : (
        !TERMINAL.includes(job.state) && <p className={styles.muted}>{t('polling')}</p>
      )}
      {notCancellable ? (
        <p className={styles.text} role="status">
          {t('not_cancellable')}
        </p>
      ) : (
        // 404 — текст «задачи нет» покажет строка по перечитанному состоянию
        !isApiError(cancel.error, 'geo.import_job_not_found') && <FormError error={cancel.error} />
      )}
      <div className={styles.actions}>
        {(stopped || failed) && (
          <Button variant="secondary" loading={checking} onClick={onCheck}>
            {t('check')}
          </Button>
        )}
        {canRun && job.state === 'queued' && (
          <Button
            variant="danger-outline"
            aria-label={t('cancel_label', { id: job.job_id })}
            loading={cancel.isPending}
            onClick={() =>
              cancel.mutate(undefined, {
                onSuccess: () => {
                  toast.success(t('cancelled'));
                  onCancelled();
                },
              })
            }
          >
            {ta('cancel')}
          </Button>
        )}
        {importId && (
          <Button variant="secondary" onClick={() => onOpenReport(importId)}>
            {t('open_report')}
          </Button>
        )}
        <Button variant="link" onClick={onHide}>
          {t('hide')}
        </Button>
      </div>
    </div>
  );
}
