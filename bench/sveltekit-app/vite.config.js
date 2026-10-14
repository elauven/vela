import adapter from '@sveltejs/adapter-node';
import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';

// SvelteKit 3 takes its configuration through the Vite plugin. The public
// origin is fixed at build time (SvelteKit 3 has no ORIGIN variable); without
// it adapter-node assumes https and rejects the uploads' CSRF check.
export default defineConfig({
  plugins: [sveltekit({ adapter: adapter(), paths: { origin: process.env.ORIGIN || 'http://localhost:8082' } })],
});
