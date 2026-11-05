import { writable } from 'vela/store';

export interface Clicks {
  update(fn: (n: number) => number): void;
  subscribe(fn: (n: number) => void): () => void;
}

export const clicks: Clicks = writable(0);
