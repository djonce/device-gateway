import { defineConfig } from '@umijs/max';

export default defineConfig({
  npmClient: 'pnpm',
  routes: [{ path: '/', component: '@/pages/index' }],
  proxy: {
    '/api': {
      target: 'http://127.0.0.1:7001',
      changeOrigin: true,
    },
    '/health': {
      target: 'http://127.0.0.1:7001',
      changeOrigin: true,
    },
  },
});
