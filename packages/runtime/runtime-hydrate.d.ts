// Generated from runtime/types/ of the Vela compiler repository; do not edit.
// Types of @govela/runtime/hydrate (runtime-hydrate.js): hydration of
// server-rendered HTML and server fragments (docs/SSR.md).

import type { Component, ComponentInstance, Props, Slots } from './runtime.js';

/** The code of every hydration mismatch. */
export const MISMATCH: 'VELA-HYDRATION-MISMATCH';

/** A difference between the server HTML and the client render (X-7.5). */
export interface MismatchReport {
  code: typeof MISMATCH;
  kind: string;
  message: string;
  policy: 'repair' | 'fail' | 'production';
  /** The site of the mismatch, in development builds. */
  site?: number;
  /** `File.vela:line:column` of the site. */
  source?: string;
  /** The Go host symbol of the site. */
  host?: string;
  server?: unknown;
  client?: unknown;
}

export interface HydrateOptions {
  /** What a mismatch does in development builds; production builds always repair silently. */
  policy?: 'repair' | 'fail' | 'off';
  /** Receives every mismatch. */
  onMismatch?: (report: MismatchReport) => void;
  slots?: Slots;
}

/**
 * Adopts the HTML and payload a Go renderer wrote into container and
 * returns the component instance. Props come from the payload.
 */
export function hydrate<P extends Props>(component: Component<P, any, any>, container: Element, options?: HydrateOptions): ComponentInstance<P>;

/** Loads the lazy components the server rendered in container, before hydrate(). */
export function preloadHydration(container: Element): Promise<void>;

/**
 * A component value whose first mount adopts html, the output of the Go
 * renderer of component, for `<vela:component :this>`. Only trusted HTML.
 */
export function serverComponent<P extends Props, E extends Record<string, any>, S extends Record<string, any>>(
  component: Component<P, E, S>,
  html: string,
  options?: HydrateOptions,
): Component<P, E, S>;

/** A component value that inserts html and has no state. Only trusted HTML. */
export function staticHTML(html: string): Component<Props, {}, {}>;

/** A head tag of the application and the depth of the component it belongs to (0 for the root). */
export interface AppHeadTag {
  node: Element;
  depth?: number;
}

/**
 * Replaces the application's head tags (a router's title and meta tags)
 * with tags. A tag of a component's `<vela:head>` with the same key wins
 * when its component is as deep or deeper; the components' tags stay.
 */
export function replaceAppHead(tags: AppHeadTag[]): void;
