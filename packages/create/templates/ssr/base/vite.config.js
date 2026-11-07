import vela from '@govela/unplugin/vite';

export default {
  plugins: [vela()],
  server: { origin: 'http://localhost:5173' },
  build: { manifest: true, rollupOptions: { input: 'src/client.__EXT__' } },
  test: {
    // Only the sources: vela copies the modules of src, tests too, into out/.
    include: ['src/**/*.{test,spec}.{js,ts}'],
    environment: 'happy-dom',
    setupFiles: ['./vitest.setup.__EXT__'],
  },
};
