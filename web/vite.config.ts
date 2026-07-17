import { defineConfig, loadEnv } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '');
  const wsTarget = env.VITE_WS_TARGET || 'ws://localhost:1780';
  const appVersion = env.VITE_APP_VERSION || 'dev';

  return {
    plugins: [
      react(),
      {
        name: 'web-client-version',
        transformIndexHtml(html) {
          return html.replace('__APP_VERSION__', escapeHTML(appVersion));
        }
      }
    ],
    build: {
      rollupOptions: {
        output: {
          manualChunks(id) {
            if (id.endsWith('/src/protocol/generated-runtime.js')) return 'protocol-runtime';
            if (id.includes('/node_modules/protobufjs/')) return 'protobuf-runtime';
            return undefined;
          }
        }
      }
    },
    server: {
      host: '0.0.0.0',
      port: 5174,
      proxy: {
        '/ws': {
          target: wsTarget,
          ws: true,
          changeOrigin: true
        },
        '/version': {
          target: wsTarget.replace(/^ws/, 'http'),
          changeOrigin: true
        },
        '/health': {
          target: wsTarget.replace(/^ws/, 'http'),
          changeOrigin: true
        },
        '/session': {
          target: wsTarget.replace(/^ws/, 'http'),
          changeOrigin: true
        }
      }
    }
  };
});

function escapeHTML(value: string): string {
  return value.replace(/[&<>"']/g, (character) => ({
    '&': '&amp;',
    '<': '&lt;',
    '>': '&gt;',
    '"': '&quot;',
    "'": '&#39;'
  })[character] ?? character);
}
