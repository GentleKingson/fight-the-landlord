import { resolve } from 'node:path';
import { defineConfig, loadEnv } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '');
  const wsTarget = env.VITE_WS_TARGET || 'ws://localhost:1780';

  return {
    plugins: [react()],
    server: {
      fs: { allow: [process.cwd(), resolve(process.cwd(), '../internal/protocol/proto'), resolve(process.cwd(), '../internal/sound/gaming')] },
      host: '0.0.0.0',
      port: 5174,
      proxy: {
        '/api': { target: wsTarget.replace(/^ws/, 'http'), changeOrigin: true },
        '/ws': {
          target: wsTarget,
          ws: true,
          changeOrigin: true
        }
      }
    }
  };
});
