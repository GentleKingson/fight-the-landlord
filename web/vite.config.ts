import { readdir, readFile, writeFile } from 'node:fs/promises';
import { extname, join, resolve } from 'node:path';
import { brotliCompressSync, constants as zlibConstants, gzipSync } from 'node:zlib';
import { defineConfig, loadEnv, type Plugin } from 'vite';
import react from '@vitejs/plugin-react';

const COMPRESSIBLE_EXTENSIONS = new Set([
  '.html',
  '.js',
  '.mjs',
  '.css',
  '.svg',
  '.json',
  '.webmanifest',
  '.txt',
  '.xml',
  '.wasm'
]);

async function collectFiles(dir: string): Promise<string[]> {
  const entries = await readdir(dir, { withFileTypes: true });
  const files: string[] = [];
  for (const entry of entries) {
    const fullPath = join(dir, entry.name);
    if (entry.isDirectory()) {
      files.push(...(await collectFiles(fullPath)));
    } else if (entry.isFile()) {
      files.push(fullPath);
    }
  }
  return files;
}

function precompressAssets(): Plugin {
  let outDir = '';
  return {
    name: 'precompress-assets',
    apply: 'build',
    configResolved(config) {
      outDir = resolve(config.root, config.build.outDir);
    },
    async closeBundle() {
      if (!outDir) return;
      const files = await collectFiles(outDir);
      await Promise.all(
        files.map(async (filePath) => {
          const ext = extname(filePath).toLowerCase();
          if (!COMPRESSIBLE_EXTENSIONS.has(ext)) return;

          const source = await readFile(filePath);
          if (source.byteLength === 0) return;

          const br = brotliCompressSync(source, {
            params: {
              [zlibConstants.BROTLI_PARAM_QUALITY]: zlibConstants.BROTLI_MAX_QUALITY,
              [zlibConstants.BROTLI_PARAM_SIZE_HINT]: source.byteLength
            }
          });
          if (br.byteLength < source.byteLength) {
            await writeFile(`${filePath}.br`, br);
          }

          const gz = gzipSync(source, { level: zlibConstants.Z_BEST_COMPRESSION });
          if (gz.byteLength < source.byteLength) {
            await writeFile(`${filePath}.gz`, gz);
          }
        })
      );
    }
  };
}

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '');
  const wsTarget = env.VITE_WS_TARGET || 'ws://localhost:1780';

  return {
    plugins: [react(), precompressAssets()],
    server: {
      fs: { allow: [process.cwd(), resolve(process.cwd(), '../internal/protocol/proto'), resolve(process.cwd(), '../internal/sound/gaming')] },
      host: '0.0.0.0',
      port: 5174,
      proxy: {
        '/api': { target: wsTarget.replace(/^ws/, 'http'), changeOrigin: true },
        '/ws': {
          target: wsTarget,
          ws: true,
          changeOrigin: false
        }
      }
    }
  };
});
