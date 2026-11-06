import vela from '@govela/unplugin/vite';

export default {
  plugins: [vela()],
  server: { origin: 'http://localhost:5173' },
  build: { manifest: true, rollupOptions: { input: 'src/_app/client.js' } },
  test: {
    environment: 'happy-dom',
    setupFiles: ['./vitest.setup.__EXT__'],
  },
};
