import { useTranslations } from 'use-intl';
import type { Story, StoryMeta } from '../showcase/csf';
import { QrCode } from './QrCode';

// QR секрета второго фактора (экран настройки 2FA). Секрет — образец из тестов, не настоящий.
export default { title: 'UI/QrCode', visual: true } satisfies StoryMeta;

const URI =
  'otpauth://totp/WF%20admin:olga.v%40wf.example?secret=JBSWY3DPEHPK3PXP&issuer=WF%20admin';

export const Default: Story = () => {
  const t = useTranslations('auth.setup');
  return <QrCode value={URI} label={t('qr')} />;
};
