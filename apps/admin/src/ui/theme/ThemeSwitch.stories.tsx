import type { Story, StoryMeta } from '../../showcase/csf';
import { ThemeSwitch } from './ThemeSwitch';

// Переключатель темы (план UF1b.6): нажат сегмент текущего выбора — витрина и снимки ставят тему через
// wf.theme (admin:shots --themes light,dark), поэтому в светлой нажата «Светлая», в тёмной — «Тёмная»;
// клик меняет тему всей витрины.
export default { title: 'UI/ThemeSwitch', visual: true } satisfies StoryMeta;

export const Default: Story = () => <ThemeSwitch />;
Default.storyName = 'theme switch';
