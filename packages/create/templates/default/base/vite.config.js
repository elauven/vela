import vela from '@govela/unplugin/vite';

export default {
  plugins: [vela()],
  test: {
    environment: 'happy-dom',
    setupFiles: ['./vitest.setup.__EXT__'],
  },
};
