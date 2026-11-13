import { useEffect, useRef } from 'react';
import { useForm } from 'react-hook-form';
import { useTranslations } from 'use-intl';
import type { Story, StoryMeta } from '../../showcase/csf';
import { Button } from '../Button';
import { cx } from '../cx';
import { Form } from '../form/Form';
import { TextField } from '../form/TextField';
import { Drawer } from '../overlay/Drawer';
import shell from '../shell/AppShell.module.css';
import { DataTable } from './DataTable';
import styles from './FocusStories.module.css';
import type { Column } from './model';

// Приёмка «фокус не закрыт» (WCAG 2.4.11, спека UI-фундамента §4.5, план UF2b.2): фокус ставит сама история —
// снимок показывает, что элемент с фокусом виден целиком, не под шапкой таблицы, подвалом панели, таббаром и
// тостом. Отступы прокрутки — scroll-padding из токенов (страж layout-css.test.ts), здесь — как это выглядит.
export default { title: 'UI/FocusNotObscured', visual: true, layout: true } satisfies StoryMeta;

type Row = { id: string; name: string };
// сотрудники — данные (как в фикстурах тестов), не тексты интерфейса
const ROWS: Row[] = Array.from({ length: 30 }, (_, i) => ({
  id: String(i + 1),
  name: `Сотрудник ${i + 1}`,
}));

function useColumns(): Column<Row>[] {
  const t = useTranslations();
  return [
    { id: 'person', header: t('staff.list.columns.person'), cell: (r) => r.name, width: 'lg' },
    { id: 'role', header: t('staff.list.columns.role'), cell: () => t('roles.moderator') },
  ];
}

function Table({ rows = ROWS }: { rows?: Row[] }) {
  const t = useTranslations('staff.list');
  return (
    <DataTable<Row>
      label={t('label')}
      columns={useColumns()}
      rowKey={(r) => r.id}
      rows={rows}
      onRetry={() => undefined}
      onRowClick={() => undefined}
      stickyFirstColumn
    />
  );
}

// Tab снизу вверх: строка 11 — сразу под шапкой, фокус уходит на строку 10 (закрыта шапкой) — прокрутка ставит её
// под закреплённую шапку с отступом size.table-header, строка видна целиком.
export const FocusUnderHeader: Story = () => {
  const page = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const scroll = page.current?.querySelector<HTMLElement>('[data-table-scroll]');
    if (!scroll) return;
    // второй запуск эффекта (StrictMode): focus() уже сфокусированного не прокручивает — снять и поставить снова
    if (document.activeElement instanceof HTMLElement) document.activeElement.blur();
    // строка 11 — сразу под шапкой, строка 10 — под ней (закрыта); Shift+Tab с 11 на 10 — фокус на закрытую
    const rows = scroll.querySelectorAll<HTMLElement>('tbody tr');
    const head = scroll.querySelector<HTMLElement>('thead')?.offsetHeight ?? 0;
    scroll.scrollTop = (rows[10]?.offsetTop ?? 0) - head;
    // Chrome при focus() ставит закрытую строку в центр (scroll-padding не виден); прокрутка «ближайшим краем»,
    // как у Firefox при Tab, — scrollIntoView nearest: строка встаёт под шапку с отступом size.table-header
    rows[9]?.focus({ preventScroll: true });
    rows[9]?.scrollIntoView({ block: 'nearest' });
  }, []);
  return (
    <div ref={page} className={styles.page}>
      <Table />
    </div>
  );
};
FocusUnderHeader.storyName = 'Focus under sticky header';

// Последнее поле длинной формы в панели: над закреплённым подвалом, не под ним.
export const LastFieldAboveFooter: Story = () => {
  const t = useTranslations();
  const form = useForm();
  useEffect(() => {
    if (document.activeElement instanceof HTMLElement) document.activeElement.blur();
    document.querySelector<HTMLElement>('input[name="field11"]')?.focus();
  }, []);
  return (
    <Drawer
      open
      onClose={() => undefined}
      title={t('staff.drawer.title')}
      footer={
        <>
          <Button variant="secondary">{t('actions.cancel')}</Button>
          <Button type="submit" form="focus-form">
            {t('actions.save')}
          </Button>
        </>
      }
    >
      <Form id="focus-form" form={form} onSubmit={() => undefined}>
        {Array.from({ length: 12 }, (_, i) => (
          <TextField
            key={i}
            name={`field${i}`}
            label={`${t('staff.list.columns.person')} ${i + 1}`}
          />
        ))}
      </Form>
    </Drawer>
  );
};
LastFieldAboveFooter.storyName = 'Last field above panel footer';

// Телефон (390): фокус на последней строке-карточке, пока на экране тост с «Отменить» — над таббаром и
// тостом (корень с data-toast-open; атрибут ставит UF2b.6, здесь — сама история).
export const LastRowWithToast: Story = () => {
  const t = useTranslations();
  const page = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const root = document.documentElement;
    root.dataset.toastOpen = '';
    if (document.activeElement instanceof HTMLElement) document.activeElement.blur();
    const rows = page.current?.querySelectorAll<HTMLElement>('tbody tr');
    rows?.[rows.length - 1]?.focus();
    return () => {
      delete root.dataset.toastOpen;
    };
  }, []);
  return (
    // область страницы каркаса телефона (.mobile .main): нижний запас под таббар и тост берётся из правила
    // каркаса при data-toast-open, а не повтором числа — поменяется формула каркаса, снимок это покажет
    <div className={shell.mobile}>
      <div ref={page} className={shell.main}>
        <Table rows={ROWS.slice(0, 12)} />
        <nav className={cx(shell.tabbar, styles.tabbar)} aria-label={t('nav.label')} />
        <div className={styles.toast} role="status">
          <span className={styles.toastText}>{t('staff.status.disabled')}</span>
          <Button variant="link">{t('actions.undo')}</Button>
        </div>
      </div>
    </div>
  );
};
LastRowWithToast.storyName = 'Last row with undo toast on phone';
