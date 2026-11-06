import vela from '@govela/unplugin/vite';

export default {
  plugins: [vela()],
  server: { origin: 'http://localhost:5173' },
  build: { manifest: true, rollupOptions: { input: 'src/client.__EXT__' } },
  test: {
    environment: 'happy-dom',
    setupFiles: ['./vitest.setup.__EXT__'],
  },
};
