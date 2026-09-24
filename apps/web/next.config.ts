import type { NextConfig } from 'next';
import createNextIntlPlugin from 'next-intl/plugin';

// /v1/* проксируется в публичный API: браузер ходит на свой origin, CORS не нужен.
const apiOrigin = process.env.WF_API_ORIGIN ?? 'http://127.0.0.1:8080';

const config: NextConfig = {
  transpilePackages: ['@wf/i18n', '@wf/tokens'],
  async rewrites() {
    return [{ source: '/v1/:path*', destination: `${apiOrigin}/v1/:path*` }];
  },
};

export default createNextIntlPlugin()(config);
