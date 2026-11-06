import vela from '@govela/unplugin/vite';

export default {
  plugins: [vela()],
  server: { origin: 'http://localhost:5173' },
  build: { manifest: true, rollupOptions: { input: 'src/client.js' } },
  test: {
    environment: 'happy-dom',
    setupFiles: ['./vitest.setup.js'],
  },
};
