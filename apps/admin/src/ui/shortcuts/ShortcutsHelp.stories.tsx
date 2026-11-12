import { useState } from 'react';
import { useTranslations } from 'use-intl';
import { Button } from '../Button';
import type { Story, StoryMeta } from '../../showcase/csf';
import { parseCombo } from './combo';
import { ShortcutsHelp, type ShortcutsHelpItem } from './ShortcutsHelp';

// Шпаргалка горячих клавиш: строки — как из реестра («/», g x, «?», Esc), односимвольные включены и
// выключены. ShortcutsProvider — без вида: в витрине шпаргалка на готовых строках.
export default { title: 'UI/Shortcuts/ShortcutsHelp', visual: true } satisfies StoryMeta;

function useItems(): ShortcutsHelpItem[] {
  const t = useTranslations();
  const row = (combo: string, label: string) => ({
    id: combo,
    label,
    steps: parseCombo(combo).steps,
  });
  return [
    row('/', t('ui.select.search')),
    row('g c', t('nav.cities')),
    row('g a', t('nav.audit')),
    row('?', t('ui.shortcuts.help')),
  ];
}

function Sample({ single: initial }: { single: boolean }) {
  const t = useTranslations('ui.shortcuts');
  const [open, setOpen] = useState(true);
  const [single, setSingle] = useState(initial);
  return (
    <>
      <Button variant="secondary" onClick={() => setOpen(true)}>
        {t('title')}
      </Button>
      <ShortcutsHelp
        open={open}
        onClose={() => setOpen(false)}
        items={useItems()}
        single={single}
        onSingleChange={setSingle}
      />
    </>
  );
}

export const Default: Story = () => <Sample single />;
export const SingleKeysOff: Story = () => <Sample single={false} />;
