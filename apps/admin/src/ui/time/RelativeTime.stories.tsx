import type { Story, StoryMeta } from '../../showcase/csf';
import { SHOWCASE_NOW } from '../../showcase/params';
import styles from '../../showcase/Stories.module.css';
import { RelativeTime } from './RelativeTime';
import { NowProvider } from './useNow';

// Относительное время (спека UI-фундамента §4.7): «только что», минуты, часы, дни, старше недели — дата, будущее,
// точное. «Сейчас» — SHOWCASE_NOW (снимки не плывут; обёртка витрины фиксирует его и сама — здесь явно).
export default { title: 'UI/RelativeTime', visual: true } satisfies StoryMeta;

const MIN = 60_000;
const at = (ms: number) => new Date(SHOWCASE_NOW.getTime() + ms).toISOString();

export const Modes: Story = () => (
  <NowProvider now={SHOWCASE_NOW}>
    <ul className={styles.column}>
      {[-20_000, -5 * MIN, -3 * 60 * MIN, -2 * 24 * 60 * MIN, -10 * 24 * 60 * MIN, 30 * MIN].map(
        (ms) => (
          <li key={ms}>
            <RelativeTime value={at(ms)} />
          </li>
        ),
      )}
      <li>
        <RelativeTime value={at(-10 * 24 * 60 * MIN)} mode="relative" />
      </li>
      <li>
        <RelativeTime value={at(-5 * MIN)} mode="exact" />
      </li>
    </ul>
  </NowProvider>
);
