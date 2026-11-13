import { useTranslations } from 'use-intl';
import { ApiError } from '../../api/client';
import type { Me } from '../../app/permissions';
import { SessionFrame } from '../../showcase/frames';
import type { Story, StoryMeta } from '../../showcase/csf';
import { meOwner } from '../../test/me';
import type { BulkReport } from './bulk';
import { BulkBar } from './BulkBar';
import type { BulkAction } from './model';

// Панель массовых действий (спека UI-фундамента §4.5): «Выбрано N», действие доступно, если разрешено
// хоть для одной строки; итог по видам с ошибками и «Повторить неудавшиеся».
export default { title: 'UI/BulkBar', visual: true, layout: true } satisfies StoryMeta;

type Row = { id: string; name: string; perm: string };

// сотрудники — данные (как в фикстурах тестов), не тексты интерфейса
const ROWS: Row[] = [
  { id: '1', name: 'Мария К.', perm: 'staff.roles.revoke' },
  { id: '2', name: 'Иван П.', perm: 'staff.roles.revoke' },
  { id: '3', name: 'Пётр С.', perm: 'staff.roles.revoke' },
  { id: '4', name: 'Ольга В.', perm: 'story.denied' },
  { id: '5', name: 'Анна Л.', perm: 'story.denied' },
];
const DENIED = ROWS.slice(3);

const me: Me = meOwner;

function useDisable(): BulkAction<Row> {
  const t = useTranslations('staff.drawer.disable');
  return {
    id: 'disable',
    label: t('button'),
    tone: 'danger',
    permission: (r) => ({ perm: r.perm }),
    confirm: { title: () => t('title') },
    run: () => Promise.resolve('executed'),
  };
}

function Frame({ rows, report }: { rows: Row[]; report?: BulkReport<Row> }) {
  const action = useDisable();
  return (
    <SessionFrame me={me}>
      <BulkBar
        rows={rows}
        rowLabel={(r) => r.name}
        actions={[action]}
        onClear={() => undefined}
        onPending={() => undefined}
        preview={report && { actionId: action.id, report }}
      />
    </SessionFrame>
  );
}

export const Selected: Story = () => <Frame rows={ROWS} />;
Selected.storyName = 'Selected';

export const NoneAllowed: Story = () => <Frame rows={DENIED} />;
NoneAllowed.storyName = 'None allowed';

const REPORT: BulkReport<Row> = {
  executed: [ROWS[0] as Row],
  pending: [ROWS[1] as Row],
  failed: [{ row: ROWS[2] as Row, error: new ApiError({ status: 409, code: 'conflict' }) }],
  unknown: [{ row: ROWS[3] as Row, key: '0192f1c4-0000-7000-8000-000000000001' }],
  skipped: [ROWS[4] as Row],
  cancelled: [],
};

export const Report: Story = () => <Frame rows={ROWS} report={REPORT} />;
Report.storyName = 'Report with failures';
