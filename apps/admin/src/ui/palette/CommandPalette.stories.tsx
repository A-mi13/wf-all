import { useState } from 'react';
import type { CommandContext } from '../../app/commands';
import { meModerator, meOwner } from '../../test/me';
import { SessionFrame } from '../../showcase/frames';
import type { Story, StoryMeta } from '../../showcase/csf';
import { CommandPalette } from './CommandPalette';

// Командная палитра (спека UI-фундамента §4.1): пустой запрос — недавнее и разделы по правам сотрудника. Сущности
// ищет сервер — в витрине сети нет, их состояния показывают тесты и снимки экранов (admin:shots).
export default {
  title: 'UI/Palette/CommandPalette',
  visual: true,
  layout: true,
} satisfies StoryMeta;

const noop = () => {};
const context: CommandContext = {
  navigate: noop,
  setTheme: noop,
  setLocale: noop,
  setDensity: noop,
  openShortcuts: noop,
  logout: noop,
};

function Sample() {
  const [open, setOpen] = useState(true);
  return <CommandPalette open={open} onClose={() => setOpen(false)} context={context} />;
}

export const Owner: Story = () => (
  <SessionFrame me={meOwner}>
    <Sample />
  </SessionFrame>
);
Owner.storyName = 'Owner';

export const Moderator: Story = () => (
  <SessionFrame me={meModerator}>
    <Sample />
  </SessionFrame>
);
Moderator.storyName = 'Moderator';
